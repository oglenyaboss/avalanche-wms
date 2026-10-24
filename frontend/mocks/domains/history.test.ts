import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toHex, type Hex } from "viem";

import { api, unwrap, type Schema } from "@/lib/api/client";
import { toApiError } from "@/lib/api/errors";
import { clearSession, getSession } from "@/lib/auth/session";
import { detailsHash, itemId } from "@/lib/signing/adr";
import { signAndSubmit, type SignedSubmit } from "@/lib/signing/sign-and-submit";
import { getAssertion } from "@/lib/signing/webauthn";

import { setChainMode } from "../chain";
import { db } from "../db";
import { assemblyIds } from "../data/fixtures/assembly";
import { historyIds } from "../data/fixtures/history";
import { passkeysIds } from "../data/fixtures/passkeys";
import { putawayIds } from "../data/fixtures/putaway";
import { shippingIds } from "../data/fixtures/shipping";
import { idGroup, mockId, seedIds, seededCredentialIds } from "../data/fixture-kit";
import { loginAs, setupMockApi, waitForChain } from "../testing";

setupMockApi();

const T0 = "2026-10-25T12:00:00.000Z";
const A = historyIds.action;
const P = (k: number) => historyIds.products[`QR-HIS-000${k}` as keyof typeof historyIds.products];
const op = seedIds.users.operator;
const opKey = () => db().passkey_credentials.find((row) => row.credential_id === seededCredentialIds.operator)!.key_id;
const tx = (n: number) => `0x${n.toString(16).padStart(64, "0")}`;
const byItem = (ids: string[]) => [...ids].sort((a, b) => (itemId(a) < itemId(b) ? -1 : itemId(a) > itemId(b) ? 1 : 0));

const summary = () => unwrap(api.GET("/analytics/summary"));
const onchain = () => unwrap(api.GET("/analytics/onchain"));
const throughput = (days?: number | string) =>
  unwrap(api.GET("/analytics/throughput", { params: { query: days === undefined ? undefined : { days: days as never } } }));
const recent = (limit?: number | string) => unwrap(api.GET("/products/recent", { params: { query: limit === undefined ? undefined : { limit: limit as never } } }));
const timeline = (key?: string) =>
  unwrap(api.GET("/products/timeline", { params: { query: (key === undefined ? {} : { key }) as { key: never } } }));
const audit = (key: string) => unwrap(api.GET("/audit/products/{key}", { params: { path: { key } } }));

const slashBody = async <T>(path: string, query = "") => {
  const response = await fetch(`${location.origin}/api${path}${query}`, {
    headers: { Authorization: `Bearer ${getSession()?.access_token}` },
  });
  const body = (await response.json()) as { data: T; error: { code: string; message: string } | null };
  if (!response.ok) throw toApiError(response.status, body.error);
  return body.data;
};

const frozen = (name: string, tests: () => void) =>
  describe(name, () => {
    beforeEach(() => {
      vi.setSystemTime(new Date(T0));
    });
    afterEach(() => {
      vi.useRealTimers();
    });
    tests();
  });

const putawayDraft = (product_ids: string[], storage_bin_id = seedIds.bins["A-01-01"]) =>
  unwrap(api.POST("/putaway/scan-storage-bin/draft", { body: { product_ids, storage_bin_id } }));
const putawaySubmit = (body: SignedSubmit) => unwrap(api.POST("/putaway/scan-storage-bin/submit", { body }));
const place = (productIds: string[]) => putawayDraft(productIds).then((draft) => signAndSubmit(draft, putawaySubmit));

const draftRegister = (target_key_id: string) => unwrap(api.POST("/passkeys/actions/key-register/draft", { body: { target_key_id } }));
const submitKeyAction = (action_id: string, body: Schema<"WebAuthnAssertion">) =>
  api.POST("/passkeys/actions/{action_id}/submit", { params: { path: { action_id } }, body });
const signKeyAction = async (draft: Schema<"KeyActionDraft">) => unwrap(submitKeyAction(draft.action_id, await getAssertion(draft)));

const countsOf = (buckets: Schema<"AnalyticsStatusBucket">[], status: string) => buckets.find((bucket) => bucket.status === status)?.count ?? 0;

describe("summary", () => {
  it("сумма бакетов сходится с totals; statuses отсортированы, нулей нет", async () => {
    await loginAs("operator");
    const result = await summary();
    for (const buckets of [result.products_by_status, result.orders_by_status, result.dispatches_by_status]) {
      const statuses = buckets.map((bucket) => bucket.status);
      expect(statuses).toEqual([...statuses].sort());
      expect(buckets.every((bucket) => bucket.count > 0)).toBe(true);
    }
    const sum = (buckets: { count: number }[]) => buckets.reduce((acc, bucket) => acc + bucket.count, 0);
    expect(sum(result.products_by_status)).toBe(result.totals.products);
    expect(sum(result.orders_by_status)).toBe(result.totals.orders);
    expect(sum(result.dispatches_by_status)).toBe(result.totals.dispatches);
    expect(result.totals.destinations).toBe(db().destinations.length);
    expect(result.totals.skus).toBe(db().skus.length);
  });

  it("раскладка: STORED +2, RECEIVED −2, events_today +1", async () => {
    await loginAs("operator");
    const before = await summary();
    await place([putawayIds.products["QR-PUT-0001"], putawayIds.products["QR-PUT-0002"]]);
    const after = await summary();
    expect(countsOf(after.products_by_status, "STORED")).toBe(countsOf(before.products_by_status, "STORED") + 2);
    expect(countsOf(after.products_by_status, "RECEIVED")).toBe(countsOf(before.products_by_status, "RECEIVED") - 2);
    expect(after.events_today).toBe(before.events_today + 1);
  });

  it("auditor — 403; без входа — 401; слеш отдаёт то же тело", async () => {
    await loginAs("auditor");
    await expect(summary()).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Требуется роль администратора или оператора" });
    clearSession();
    await expect(unwrap(api.GET("/analytics/summary"))).rejects.toMatchObject({ status: 401 });
    await loginAs("admin");
    const plain = await summary();
    expect(await slashBody<Schema<"AnalyticsSummaryResponse">>("/analytics/summary/")).toEqual(plain);
  });
});

frozen("summary с замороженными сутками", () => {
  it("events_today = total_events − 9 (девять событий истории до сегодняшних суток)", async () => {
    await loginAs("operator");
    const result = await summary();
    const hero = await onchain();
    expect(result.events_today).toBe(hero.total_events - 9);
  });
});

describe("onchain", () => {
  it("этапы, суммы, rate и ленты", async () => {
    await loginAs("operator");
    const result = await onchain();
    expect(result.by_stage.map((stage) => stage.aggregate_type)).toEqual(["receiving", "putaway", "picking", "shipping"]);
    expect(result.by_stage.reduce((acc, stage) => acc + stage.total, 0)).toBe(result.total_events);
    expect(result.committed + result.sent + result.failed + result.pending).toBe(result.total_events);
    expect(result.confirmation_rate).toBe(result.committed / result.total_events);
    for (const feed of [result.recent_failed, result.recent_committed]) {
      expect(feed.length).toBeLessThanOrEqual(10);
      for (let i = 1; i < feed.length; i++) expect(feed[i - 1]!.updated_at >= feed[i]!.updated_at).toBe(true);
    }
    expect(result.recent_failed).toContainEqual({
      event_id: A(607),
      aggregate_type: "putaway",
      tx_hash: tx(607),
      error_message: "rejected by contract",
      updated_at: expect.stringMatching(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/),
    });
  });

  it("hold держит PENDING, потом commit даёт committed +1", async () => {
    await loginAs("operator");
    const before = await onchain();
    setChainMode("hold");
    await place([putawayIds.products["QR-PUT-0001"]]);
    const held = await onchain();
    expect(held.total_events).toBe(before.total_events + 1);
    expect(held.pending).toBe(before.pending + 1);
    expect(held.by_stage.find((stage) => stage.aggregate_type === "putaway")!.total).toBe(before.by_stage.find((stage) => stage.aggregate_type === "putaway")!.total + 1);
    expect(held.by_stage.find((stage) => stage.aggregate_type === "putaway")!.pending).toBe(before.by_stage.find((stage) => stage.aggregate_type === "putaway")!.pending + 1);

    setChainMode("commit");
    await place([putawayIds.products["QR-PUT-0002"]]);
    await waitForChain();
    expect(held.committed).toBe(before.committed);
    expect((await onchain()).committed).toBe(held.committed + 1);
  });

  it("сквозной ключ: KeyRegister попадает в этап keys в onchain и throughput", async () => {
    await loginAs("admin");
    await signKeyAction(await draftRegister(passkeysIds.keys.operator3.keyId));
    const result = await onchain();
    expect(result.by_stage.at(-1)).toMatchObject({ aggregate_type: "keys", total: 1 });
    const daily = await throughput(1);
    expect(daily.series.find((stage) => stage.aggregate_type === "keys")).toMatchObject({ counts: [1] });
  });
});

frozen("throughput с замороженными сутками", () => {
  it("ось 14 дней, канонические полосы, счётчики истории", async () => {
    await loginAs("operator");
    const result = await throughput();
    expect(result.days).toHaveLength(14);
    expect(result.days[0]).toBe("2026-10-12");
    expect(result.days[13]).toBe("2026-10-25");
    expect(result.series.map((stage) => stage.aggregate_type)).toEqual(["receiving", "putaway", "picking", "shipping"]);
    const counts = (stage: string) => result.series.find((band) => band.aggregate_type === stage)!.counts;
    expect(counts("receiving").slice(0, 13)).toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0]);
    expect(counts("putaway").slice(0, 13)).toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 2]);
    expect(counts("picking").slice(0, 13)).toEqual([1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1]);
    expect(counts("shipping").slice(0, 13)).toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
    expect(result.totals.slice(0, 13)).toEqual([1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3]);
    expect(result.totals[13]).toBe(counts("receiving")[13]! + counts("putaway")[13]! + counts("picking")[13]! + counts("shipping")[13]!);
    expect(counts("shipping")[13]).toBeGreaterThanOrEqual(1);
    expect(counts("putaway")[13]).toBeGreaterThanOrEqual(1);
  });

  it("days=30/1/90/91/0/−3/abc/1.5/пусто/+7", async () => {
    await loginAs("operator");
    const d30 = await throughput(30);
    expect(d30.days[0]).toBe("2026-09-26");
    expect(d30.series.find((band) => band.aggregate_type === "receiving")!.counts[d30.days.indexOf("2026-10-05")]).toBe(1);
    expect(d30.series.find((band) => band.aggregate_type === "putaway")!.counts[d30.days.indexOf("2026-10-11")]).toBe(1);
    const d1 = await throughput(1);
    expect(d1.days).toEqual(["2026-10-25"]);
    expect(d1.series).toHaveLength(4);
    for (const band of d1.series) expect(band.counts).toHaveLength(1);
    expect((await throughput(90)).days).toHaveLength(90);
    for (const days of [91, 0, -3]) {
      await expect(throughput(days)).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "days должен быть в диапазоне 1..90" });
    }
    for (const days of ["abc", 1.5]) {
      await expect(throughput(days as never)).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "days должен быть числом" });
    }
    expect((await throughput("")).days).toHaveLength(14);
    expect((await throughput("+7")).days).toHaveLength(7);
  });
});

describe("recent", () => {
  it("limit: 3 → 3; 100 и 50 — потолок 50; мусор и пустое — 20", async () => {
    await loginAs("operator");
    expect(await recent(3)).toHaveLength(3);
    expect(await recent(100)).toHaveLength(Math.min(50, db().products.length));
    expect(await recent(50)).toHaveLength(Math.min(50, db().products.length));
    for (const limit of [0, -1, "abc", ""]) expect(await recent(limit as never)).toHaveLength(20);
  });

  it("раскладка делает товар первым", async () => {
    await loginAs("operator");
    await place([putawayIds.products["QR-PUT-0001"]]);
    const result = await recent();
    expect(result[0]).toMatchObject({ qr_code: "QR-PUT-0001", status: "STORED" });
  });

  it("auditor — 403", async () => {
    await loginAs("auditor");
    await expect(recent()).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Требуется роль администратора или оператора" });
  });
});

frozen("recent с замороженными сутками", () => {
  it("топ по updated_at: история впереди, литерал первого элемента", async () => {
    await loginAs("operator");
    const result = await recent();
    expect(result).toHaveLength(20);
    for (let i = 1; i < result.length; i++) expect(result[i - 1]!.updated_at >= result[i]!.updated_at).toBe(true);
    expect(result.slice(0, 3).map((row) => row.qr_code)).toEqual(["QR-HIS-0001", "QR-HIS-0002", "QR-HIS-0003"]);
    expect(result[0]).toEqual({
      product_id: P(1),
      qr_code: "QR-HIS-0001",
      sku_name: "Куртка Columbia Watertight",
      status: "SHIPPED",
      updated_at: "2026-10-25T11:58:00Z",
    });
  });
});

describe("timeline", () => {
  it("QR-HIS-0003: второй шаг FAILED с причиной", async () => {
    await loginAs("operator");
    const result = await timeline("QR-HIS-0003");
    const step = result.steps[1]!;
    expect(step.event_id).toBe(A(607));
    expect(step.stage).toBe("putaway");
    expect(step.chain_status).toBe("FAILED");
    expect(step.error_message).toBe("rejected by contract");
  });

  it("key: UUID в верхнем регистре и пробелы — тот же ответ; QR-HIS-0006 без событий", async () => {
    await loginAs("operator");
    const byQr = await timeline("QR-HIS-0001");
    expect(await timeline(P(1).toUpperCase())).toEqual(byQr);
    expect(await timeline(`  QR-HIS-0001 `)).toEqual(byQr);
    expect(await timeline("QR-HIS-0006")).toMatchObject({ steps: [] });
  });

  it.each([
    ["key отсутствует", undefined],
    ["key пустой", ""],
    ["key из пробелов", "  "],
  ])("%s — 400 MISSING_KEY", async (_, key) => {
    await loginAs("operator");
    await expect(timeline(key as never)).rejects.toMatchObject({ status: 400, code: "MISSING_KEY", message: "Укажите product_id или QR-код" });
  });

  it("неизвестный qr и uuid — 404 PRODUCT_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(timeline("QR-NOPE")).rejects.toMatchObject({ status: 404, code: "PRODUCT_NOT_FOUND", message: "Товар не найден" });
    await expect(timeline(mockId(idGroup.product, 999999))).rejects.toMatchObject({ status: 404, code: "PRODUCT_NOT_FOUND", message: "Товар не найден" });
  });

  it("черновик не виден в таймлайне; после отправки PENDING → COMMITTED", async () => {
    await loginAs("operator");
    const draft = await putawayDraft([putawayIds.products["QR-PUT-0001"]]);
    expect((await timeline("QR-PUT-0001")).steps.map((step) => step.stage)).toEqual(["receiving"]);
    await signAndSubmit(draft, putawaySubmit);
    expect((await timeline("QR-PUT-0001")).steps.at(-1)).toMatchObject({ stage: "putaway", chain_status: "PENDING" });
    await waitForChain();
    const done = await timeline("QR-PUT-0001");
    expect(done.steps.at(-1)).toMatchObject({ chain_status: "COMMITTED" });
    expect(done.steps.at(-1)!.tx_hash).not.toBeNull();
  });

  it("auditor — 403; слеш-путь — 200", async () => {
    await loginAs("auditor");
    await expect(timeline("QR-HIS-0001")).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Требуется роль администратора или оператора" });
    await loginAs("operator");
    const body = await slashBody<Schema<"ProductTimeline">>("/products/timeline/", "?key=QR-HIS-0001");
    expect(body.product.qr_code).toBe("QR-HIS-0001");
  });
});

frozen("timeline с замороженными сутками", () => {
  it("QR-HIS-0001: четыре шага с литералами", async () => {
    await loginAs("operator");
    const result = await timeline("QR-HIS-0001");
    expect(result.product).toEqual({ product_id: P(1), qr_code: "QR-HIS-0001", sku_name: "Куртка Columbia Watertight", status: "SHIPPED" });
    expect(result.steps.map((step) => step.stage)).toEqual(["receiving", "putaway", "picking", "shipping"]);
    expect(result.steps.map((step) => step.event_id)).toEqual([A(601), A(602), A(603), A(604)]);
    expect(result.steps.map((step) => step.occurred_at)).toEqual(["2026-10-22T12:00:00Z", "2026-10-23T12:00:00Z", "2026-10-24T12:00:00Z", "2026-10-25T11:50:00Z"]);
    expect(result.steps.map((step) => step.chain_status)).toEqual(["COMMITTED", "COMMITTED", "COMMITTED", "SENT"]);
    expect(result.steps.map((step) => step.chain_updated_at)).toEqual(result.steps.map((step) => step.occurred_at));
    expect(result.steps.map((step) => step.event_type)).toEqual(["wms.action.v1", "wms.action.v1", "wms.action.v1", "wms.action.v1"]);
    expect(result.steps.map((step) => step.error_message)).toEqual([null, null, null, null]);
    expect(result.steps[0]!.tx_hash).toBe(tx(601));
  });

  it("QR-HIS-0002: 606 без строки цепи — PENDING без хеша", async () => {
    await loginAs("operator");
    const result = await timeline("QR-HIS-0002");
    expect(result.steps.map((step) => step.stage)).toEqual(["receiving", "putaway", "putaway"]);
    expect(result.steps[2]).toEqual({
      stage: "putaway",
      event_type: "wms.action.v1",
      occurred_at: "2026-10-25T11:30:00Z",
      event_id: A(606),
      tx_hash: null,
      chain_status: "PENDING",
      chain_updated_at: null,
      error_message: null,
    });
  });
});

frozen("audit с замороженными сутками", () => {
  it("QR-HIS-0001: четыре действия, шапка и детали", async () => {
    await loginAs("auditor");
    const result = await audit("QR-HIS-0001");
    expect(result.productId).toBe(P(1));
    expect(result.itemId).toBe(toHex(itemId(P(1)), { size: 32 }));
    expect(result.actions.map((action) => action.actionId)).toEqual([A(601), A(602), A(603), A(604)]);
    expect(result.actions.map((action) => action.itemIds)).toEqual([byItem([P(1), P(2), P(3)]), [P(1)], [P(1)], [P(1)]]);
    const key = opKey();
    for (const action of result.actions) {
      expect(action.operatorId).toBe(op);
      expect(action.operator).toBe("operator");
      expect(action.keyOwnerId).toBe(op);
      expect(action.orgId).toBe(seedIds.org);
      expect(action.orgName).toBe("Демо-склад");
      expect(action.keyId).toBe(key);
      expect(action.salt).toBe(`0x${"00".repeat(32)}`);
      expect(action.partIndex).toBe(0);
      expect(action.rejectReason).toBeNull();
      expect(action.detailsError).toBeNull();
    }
    const [a601, a602, a603, a604] = result.actions;
    expect(a601).toMatchObject({ operationId: mockId(idGroup.operation, 601), actionType: 1, submittedAt: "2026-10-22T12:00:00Z", chainStatus: "COMMITTED" });
    expect(a601!.details).toStrictEqual({ cargoplaceId: historyIds.cargoplace, operatorId: op, occurredAtMs: Date.parse("2026-10-22T12:00:00Z") });
    expect(a602!.actionType).toBe(2);
    expect(a602!.details).toStrictEqual({ storageBinId: seedIds.bins["A-01-01"], operatorId: op, occurredAtMs: Date.parse("2026-10-23T12:00:00Z") });
    expect(a603!.actionType).toBe(3);
    expect(a603!.details).toStrictEqual({
      shippingBufferBinId: seedIds.bins["BIN-SHIP-5"],
      orderIds: [historyIds.order],
      operatorId: op,
      occurredAtMs: Date.parse("2026-10-24T12:00:00Z"),
    });
    expect(a604!.actionType).toBe(4);
    expect(a604!.chainStatus).toBe("SENT");
    expect(a604!.details).toStrictEqual({
      dispatchId: shippingIds.dispatches["DSP-SHP-003"],
      shippingBufferBinId: seedIds.bins["BIN-SHIP-5"],
      orderIds: [historyIds.order],
      operatorId: op,
      occurredAtMs: Date.parse("2026-10-25T11:50:00Z"),
    });
  });

  it("QR-HIS-0002: 601/605/606, у 606 chainStatus пустой", async () => {
    await loginAs("auditor");
    const result = await audit("QR-HIS-0002");
    expect(result.actions.map((action) => action.actionId)).toEqual([A(601), A(605), A(606)]);
    expect(result.actions[1]!.details).toStrictEqual({ storageBinId: seedIds.bins["A-01-02"], operatorId: op, occurredAtMs: Date.parse("2026-10-23T11:00:00Z") });
    const a606 = result.actions[2]!;
    expect(a606.chainStatus).toBe("");
    expect(a606.rejectReason).toBeNull();
    expect(a606.details).toStrictEqual({ storageBinId: seedIds.bins["A-02-01"], operatorId: op, occurredAtMs: Date.parse("2026-10-25T11:30:00Z") });
    expect(a606.submittedAt).toBe("2026-10-25T11:30:00Z");
  });
});

describe("audit", () => {
  it("QR-HIS-0003: 607 без доменной строки — itemIds [], DOMAIN_ROW_MISSING", async () => {
    await loginAs("auditor");
    const result = await audit("QR-HIS-0003");
    const a607 = result.actions[1]!;
    expect(a607.actionId).toBe(A(607));
    expect(a607.chainStatus).toBe("FAILED");
    expect(a607.rejectReason).toBe(12);
    expect(a607.itemIds).toEqual([]);
    expect(a607.details).toBeNull();
    expect(a607.detailsError).toBe("DOMAIN_ROW_MISSING");
  });

  it("QR-HIS-0004: 608 с расходящимися ячейками — DOMAIN_INCONSISTENT", async () => {
    await loginAs("auditor");
    const result = await audit("QR-HIS-0004");
    expect(result.actions).toHaveLength(1);
    const a608 = result.actions[0]!;
    expect(a608.actionId).toBe(A(608));
    expect(a608.itemIds).toEqual([P(4), P(5)]);
    expect(a608.details).toBeNull();
    expect(a608.detailsError).toBe("DOMAIN_INCONSISTENT");
  });

  it("QR-HIS-0006 — действий нет; поиск по product_id, верхний регистр — 404", async () => {
    await loginAs("auditor");
    expect((await audit("QR-HIS-0006")).actions).toEqual([]);
    expect(await audit(P(1))).toEqual(await audit("QR-HIS-0001"));
    await expect(audit(P(1).toUpperCase())).rejects.toMatchObject({ status: 404, code: "PRODUCT_NOT_FOUND", message: "Товар не найден" });
    await expect(audit("QR-NOPE")).rejects.toMatchObject({ status: 404, code: "PRODUCT_NOT_FOUND", message: "Товар не найден" });
  });

  it("admin — 200; operator — 403", async () => {
    await loginAs("admin");
    await expect(audit("QR-HIS-0001")).resolves.toMatchObject({ productId: P(1) });
    await loginAs("operator");
    await expect(audit("QR-HIS-0001")).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Требуется роль аудитора или администратора" });
  });

  it("хеш сходится — раскладка", async () => {
    await loginAs("operator");
    const draft = await putawayDraft([putawayIds.products["QR-PUT-0001"], putawayIds.products["QR-PUT-0002"]]);
    await signAndSubmit(draft, putawaySubmit);
    await loginAs("auditor");
    const result = await audit("QR-PUT-0001");
    const action = result.actions.find((candidate) => candidate.actionId === draft.parts[0]!.action_id)!;
    expect(action.actionType).toBe(2);
    expect(action.chainStatus).toBe("PENDING");
    expect(detailsHash(2, { salt: action.salt as Hex, ...action.details! })).toBe(draft.parts[0]!.details_hash);
    expect([...action.itemIds].sort()).toEqual([...draft.parts[0]!.item_ids].sort());
    expect(action.submittedAt).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d{1,3})?Z$/);
  });

  it("хеш сходится — сборка", async () => {
    await loginAs("operator");
    const draft = await unwrap(api.POST("/assembly/scan-shipping-buffer/draft", { body: { buffer_bin_id: seedIds.bins["BIN-SHIP-7"] } }));
    await signAndSubmit(draft, (body: SignedSubmit) => unwrap(api.POST("/assembly/scan-shipping-buffer/submit", { body })));
    await loginAs("auditor");
    const result = await audit("QR-ASM-0020");
    const action = result.actions.find((candidate) => candidate.actionId === draft.parts[0]!.action_id)!;
    expect(action.actionType).toBe(3);
    expect(detailsHash(3, { salt: action.salt as Hex, ...action.details! })).toBe(draft.parts[0]!.details_hash);
    expect(action.details).toMatchObject({
      shippingBufferBinId: seedIds.bins["BIN-SHIP-7"],
      orderIds: [assemblyIds.orders["ORD-ASM-020"]],
    });
  });

  it("хеш сходится — отгрузка", async () => {
    await loginAs("operator");
    const draft = await unwrap(
      api.POST("/shipping/ship/draft", {
        body: { buffer_bin_id: seedIds.bins["BIN-SHIP-5"], dispatch_id: shippingIds.dispatches["DSP-SHP-001"], product_ids: [shippingIds.products["QR-SHP-0001"]] },
      }),
    );
    await signAndSubmit(draft, (body: SignedSubmit) => unwrap(api.POST("/shipping/ship/submit", { body })));
    await loginAs("auditor");
    const result = await audit("QR-SHP-0001");
    const action = result.actions.find((candidate) => candidate.actionId === draft.parts[0]!.action_id)!;
    expect(action.actionType).toBe(4);
    expect(detailsHash(4, { salt: action.salt as Hex, ...action.details! })).toBe(draft.parts[0]!.details_hash);
    expect(action.details).toMatchObject({
      dispatchId: shippingIds.dispatches["DSP-SHP-001"],
      shippingBufferBinId: seedIds.bins["BIN-SHIP-5"],
      orderIds: [shippingIds.orders["ORD-SHP-001"]],
    });
  });
});
