import { toHex } from "viem";

import type { Schema } from "@/lib/api/client";
import { itemId } from "@/lib/signing/adr";

import type { Db, OnchainEventRow, WarehouseActionRow } from "../db";
import { fail, guards, parseUuid, route, type Ctx } from "../http";
import { maxItemsPerAction, sortByItemId } from "../signed";

const recentFeedLimit = 10;
const defaultThroughputDays = 14;
const maxThroughputDays = 90;
const defaultRecentLimit = 20;
const maxRecentLimit = 50;

const fsmOrder = ["receiving", "putaway", "picking", "shipping"] as const;
type Stage = (typeof fsmOrder)[number];
const stageByActionType: Record<WarehouseActionRow["action_type"], Stage> = { 1: "receiving", 2: "putaway", 3: "picking", 4: "shipping" };

type StageStatus = { committed: number; sent: number; failed: number };
type DetailError = "DOMAIN_ROW_MISSING" | "DOMAIN_INCONSISTENT";
type DetailResult = { value: Schema<"AuditDetails"> } | { error: DetailError };

const rfc3339 = (iso: string) => iso.replace(/\.\d{3}Z$/, "Z");
const rfc3339Nano = (iso: string) => iso.replace(/\.?0+Z$/, "Z");
const clampNonNeg = (value: number) => (value < 0 ? 0 : value);
const confirmationRate = (committed: number, total: number) => (total > 0 ? committed / total : 0);

const integerParam = (raw: string): number | null => (/^[+-]?\d+$/.test(raw) ? Number(raw) : null);

const stageOfActionRow = (action: WarehouseActionRow): Stage => stageByActionType[action.action_type];

const stageOf = (state: Db, eventId: string): Stage | "keys" => {
  const action = state.warehouse_actions.find((row) => row.action_id === eventId);
  return action ? stageOfActionRow(action) : "keys";
};

const orderStages = (present: ReadonlySet<string>): string[] => [...fsmOrder, ...[...present].filter((stage) => !fsmOrder.includes(stage as Stage)).sort((a, b) => (a < b ? -1 : 1))];

const skuName = (state: Db, skuId: string) => state.skus.find((row) => row.sku_id === skuId)?.name ?? "";

const byStatus = (rows: { status: string }[]): Schema<"AnalyticsStatusBucket">[] => {
  const counts = new Map<string, number>();
  for (const row of rows) counts.set(row.status, (counts.get(row.status) ?? 0) + 1);
  return [...counts.entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([status, count]) => ({ status, count }));
};

const utcDayStartMs = () => Date.parse(new Date().toISOString().slice(0, 10));
const dayKey = (ms: number) => new Date(ms).toISOString().slice(0, 10);

const countStatus = (state: Db, status: OnchainEventRow["status"]) => state.onchain_events.filter((row) => row.status === status).length;

const stageAcc = (accs: Map<string, StageStatus>, stage: string): StageStatus => {
  let acc = accs.get(stage);
  if (!acc) {
    acc = { committed: 0, sent: 0, failed: 0 };
    accs.set(stage, acc);
  }
  return acc;
};

const onchainStageBreakdown = (state: Db): Schema<"OnchainStageBreakdown">[] => {
  const stageTotal = new Map<string, number>();
  const present = new Set<string>();
  for (const row of state.outbox_events) {
    const stage = stageOf(state, row.event_id);
    stageTotal.set(stage, (stageTotal.get(stage) ?? 0) + 1);
    present.add(stage);
  }
  const accs = new Map<string, StageStatus>();
  for (const row of state.onchain_events) {
    if (row.status !== "COMMITTED" && row.status !== "SENT" && row.status !== "FAILED") continue;
    const stage = stageOf(state, row.event_id);
    const acc = stageAcc(accs, stage);
    if (row.status === "COMMITTED") acc.committed += 1;
    else if (row.status === "SENT") acc.sent += 1;
    else acc.failed += 1;
    present.add(stage);
  }
  return orderStages(present).map((stage) => {
    const total = stageTotal.get(stage) ?? 0;
    const acc = accs.get(stage);
    const committed = acc?.committed ?? 0;
    const sent = acc?.sent ?? 0;
    const failed = acc?.failed ?? 0;
    return { aggregate_type: stage, total, committed, sent, pending: clampNonNeg(total - committed - sent - failed), failed };
  });
};

const recentEvents = (state: Db, status: OnchainEventRow["status"]): Schema<"OnchainEventRef">[] =>
  state.onchain_events
    .filter((row) => row.status === status)
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
    .slice(0, recentFeedLimit)
    .map((row) => ({
      event_id: row.event_id,
      aggregate_type: stageOf(state, row.event_id),
      tx_hash: row.tx_hash,
      error_message: row.error_message,
      updated_at: rfc3339(row.updated_at),
    }));

const summary = ({ db: state }: Ctx): Schema<"AnalyticsSummaryResponse"> => ({
  totals: {
    skus: state.skus.length,
    products: state.products.length,
    orders: state.orders.length,
    dispatches: state.outbound_dispatches.length,
    destinations: state.destinations.length,
  },
  events_today: state.outbox_events.filter((row) => Date.parse(row.created_at) >= utcDayStartMs()).length,
  products_by_status: byStatus(state.products),
  orders_by_status: byStatus(state.orders),
  dispatches_by_status: byStatus(state.outbound_dispatches),
});

const onchain = ({ db: state }: Ctx): Schema<"AnalyticsOnchainResponse"> => {
  const total = state.outbox_events.length;
  const committed = countStatus(state, "COMMITTED");
  const sent = countStatus(state, "SENT");
  const failed = countStatus(state, "FAILED");
  return {
    total_events: total,
    committed,
    sent,
    pending: clampNonNeg(total - committed - sent - failed),
    failed,
    confirmation_rate: confirmationRate(committed, total),
    by_stage: onchainStageBreakdown(state),
    recent_failed: recentEvents(state, "FAILED"),
    recent_committed: recentEvents(state, "COMMITTED"),
  };
};

const throughput = ({ db: state, url }: Ctx): Schema<"AnalyticsThroughputResponse"> => {
  const raw = url.searchParams.get("days");
  let days = defaultThroughputDays;
  if (raw !== null && raw !== "") {
    const parsed = integerParam(raw);
    if (parsed === null) throw fail(400, "INVALID_REQUEST", "days должен быть числом");
    if (parsed < 1 || parsed > maxThroughputDays) throw fail(400, "INVALID_REQUEST", "days должен быть в диапазоне 1..90");
    days = parsed;
  }
  const today = utcDayStartMs();
  const start = today - (days - 1) * 86_400_000;
  const axis = Array.from({ length: days }, (_, i) => dayKey(start + i * 86_400_000));
  const index = new Map(axis.map((key, i) => [key, i] as const));
  const rows = state.outbox_events
    .filter((row) => Date.parse(row.created_at) >= start)
    .map((row) => ({ stage: stageOf(state, row.event_id), day: dayKey(Date.parse(row.created_at)) }));
  const stages = orderStages(new Set(rows.map((row) => row.stage)));
  const counts = new Map(stages.map((stage) => [stage, Array.from({ length: days }, () => 0)] as const));
  const totals = axis.map(() => 0);
  for (const row of rows) {
    const i = index.get(row.day);
    if (i === undefined) continue;
    counts.get(row.stage)![i] += 1;
    totals[i] += 1;
  }
  return { days: axis, series: stages.map((stage) => ({ aggregate_type: stage, counts: counts.get(stage)! })), totals };
};

const recent = ({ db: state, url }: Ctx): Schema<"RecentProduct">[] => {
  let limit = defaultRecentLimit;
  const raw = url.searchParams.get("limit") ?? "";
  const parsed = raw === "" ? null : integerParam(raw);
  if (parsed !== null && parsed > 0) limit = parsed;
  if (limit > maxRecentLimit) limit = maxRecentLimit;
  return state.products
    .slice()
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
    .slice(0, limit)
    .map((row) => ({
      product_id: row.product_id,
      qr_code: row.qr_code,
      sku_name: skuName(state, row.sku_id),
      status: row.status,
      updated_at: rfc3339(row.updated_at),
    }));
};

const timeline = ({ db: state, url }: Ctx): Schema<"ProductTimeline"> => {
  const key = (url.searchParams.get("key") ?? "").trim();
  if (key === "") throw fail(400, "MISSING_KEY", "Укажите product_id или QR-код");
  const id = parseUuid(key);
  const row = id ? state.products.find((product) => product.product_id === id) : state.products.find((product) => product.qr_code === key);
  if (!row) throw fail(404, "PRODUCT_NOT_FOUND", "Товар не найден");
  const joined = state.outbox_events.flatMap((outbox) => {
    const action = state.warehouse_actions.find((candidate) => candidate.action_id === outbox.event_id);
    return action !== undefined && action.item_ids.includes(row.product_id) ? [{ outbox, action }] : [];
  }).sort((a, b) => a.outbox.created_at.localeCompare(b.outbox.created_at));
  const steps = joined.map(({ outbox, action }) => {
    const chain = state.onchain_events.find((event) => event.event_id === outbox.event_id) ?? null;
    return {
      stage: stageOfActionRow(action),
      event_type: outbox.event_type,
      occurred_at: rfc3339(outbox.created_at),
      event_id: action.action_id,
      tx_hash: chain?.tx_hash ?? null,
      chain_status: chain?.status ?? "PENDING",
      chain_updated_at: chain ? rfc3339(chain.updated_at) : null,
      error_message: chain?.error_message ?? null,
    };
  });
  return {
    product: { product_id: row.product_id, qr_code: row.qr_code, sku_name: skuName(state, row.sku_id), status: row.status },
    steps,
  };
};

const productNotFound = () => fail(404, "PRODUCT_NOT_FOUND", "Товар не найден");

const partItems = (sorted: string[], part: number): string[] => {
  const start = part * maxItemsPerAction;
  if (start < 0 || start >= sorted.length) return [];
  return sorted.slice(start, start + maxItemsPerAction);
};

const actionRows = <T extends { action_id: string | null }>(rows: T[], actionId: string): T[] => rows.filter((row) => row.action_id === actionId);

const boxClosed = (state: Db, boxId: string | null) => (boxId !== null ? state.boxes.find((row) => row.box_id === boxId)?.status === "CLOSED" : false);

const domainItems = (state: Db, action: WarehouseActionRow): string[] => {
  switch (action.action_type) {
    case 1: {
      const closedCargoplaces = new Set(
        state.receiving_table.filter((row) => row.operation_id === action.operation_id && row.action === "CLOSE_CARGO").map((row) => row.cargoplace_id),
      );
      const ids = state.receiving_table
        .filter((row) => row.action === "SCAN_QR" && row.product_id !== null && boxClosed(state, row.box_id) && closedCargoplaces.has(row.cargoplace_id))
        .map((row) => row.product_id!);
      return partItems(sortByItemId(ids), action.part_index);
    }
    case 2: return actionRows(state.putaways, action.action_id).map((row) => row.product_id);
    case 3: return actionRows(state.assembly_tasks, action.action_id).map((row) => row.product_id);
    case 4: return actionRows(state.shippings, action.action_id).map((row) => row.product_id);
  }
};

const distinct = <T>(values: T[]): T[] => {
  const seen = new Set<string>();
  return values.filter((value) => {
    const key = JSON.stringify(value);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
};

const detailError = (count: number): DetailError => (count === 0 ? "DOMAIN_ROW_MISSING" : "DOMAIN_INCONSISTENT");

const shippingBufferBinOf = (state: Db, action: WarehouseActionRow): { value: string } | { error: DetailError } => {
  const bins = new Set<string | null>();
  for (const id of action.item_ids) {
    const row = state.products.find((product) => product.product_id === id);
    if (row) bins.add(row.bin_id);
  }
  if (bins.has(null) || bins.size === 0) return { error: "DOMAIN_ROW_MISSING" };
  if (bins.size > 1) return { error: "DOMAIN_INCONSISTENT" };
  return { value: [...bins][0]! };
};

const detailsOf = (state: Db, action: WarehouseActionRow): DetailResult => {
  switch (action.action_type) {
    case 1: {
      const tuples = distinct(
        state.receiving_table
          .filter((row) => row.operation_id === action.operation_id && row.action === "CLOSE_CARGO")
          .map((row) => ({ cargoplaceId: row.cargoplace_id, operatorId: row.operator_id, occurredAtMs: Date.parse(row.occurred_at) })),
      );
      if (tuples.length !== 1) return { error: detailError(tuples.length) };
      return { value: { cargoplaceId: tuples[0]!.cargoplaceId, operatorId: tuples[0]!.operatorId, occurredAtMs: tuples[0]!.occurredAtMs } };
    }
    case 2: {
      const tuples = distinct(
        actionRows(state.putaways, action.action_id).map((row) => ({ storageBinId: row.bin_id, operatorId: row.operator_id, occurredAtMs: Date.parse(row.occurred_at) })),
      );
      if (tuples.length !== 1) return { error: detailError(tuples.length) };
      return { value: { storageBinId: tuples[0]!.storageBinId, operatorId: tuples[0]!.operatorId, occurredAtMs: tuples[0]!.occurredAtMs } };
    }
    case 3: {
      const rows = actionRows(state.assembly_tasks, action.action_id);
      const tuples = distinct(rows.map((row) => ({ operatorId: row.operator_id, occurredAt: row.unloaded_at })));
      if (tuples.length !== 1) return { error: detailError(tuples.length) };
      const tuple = tuples[0]!;
      if (tuple.occurredAt === null) return { error: "DOMAIN_ROW_MISSING" };
      const orderIds = distinct(rows.map((row) => row.order_id));
      if (orderIds.length === 0) return { error: "DOMAIN_ROW_MISSING" };
      const bin = shippingBufferBinOf(state, action);
      if ("error" in bin) return bin;
      return { value: { shippingBufferBinId: bin.value, orderIds, operatorId: tuple.operatorId!, occurredAtMs: Date.parse(tuple.occurredAt) } };
    }
    case 4: {
      const rows = actionRows(state.shippings, action.action_id);
      const tuples = distinct(rows.map((row) => ({ dispatchId: row.dispatch_id, operatorId: row.operator_id, occurredAtMs: Date.parse(row.occurred_at) })));
      if (tuples.length !== 1) return { error: detailError(tuples.length) };
      const tuple = tuples[0]!;
      const orderIds = action.item_ids.flatMap((id) => {
        const row = state.products.find((product) => product.product_id === id);
        return row !== undefined && row.order_id !== null ? [row.order_id] : [];
      });
      const bin = shippingBufferBinOf(state, action);
      if ("error" in bin) return bin;
      return {
        value: { dispatchId: tuple.dispatchId, shippingBufferBinId: bin.value, orderIds: distinct(orderIds), operatorId: tuple.operatorId, occurredAtMs: tuple.occurredAtMs },
      };
    }
  }
};

const actionJoined = (state: Db, action: WarehouseActionRow) => {
  const signer = state.passkey_credentials.find((row) => row.credential_id === action.signer_credential_id);
  if (!signer) return null;
  const org = state.organizations.find((row) => row.org_id === signer.org_id);
  const user = state.users.find((row) => row.user_id === action.created_by);
  if (!org || !user) return null;
  const chain = state.onchain_events.find((row) => row.event_id === action.action_id) ?? null;
  const details = detailsOf(state, action);
  return {
    actionId: action.action_id,
    operationId: action.operation_id,
    partIndex: action.part_index,
    actionType: action.action_type,
    itemIds: domainItems(state, action),
    salt: action.salt,
    submittedAt: rfc3339Nano(action.submitted_at!),
    operatorId: user.user_id,
    operator: user.username,
    keyOwnerId: signer.user_id,
    orgId: signer.org_id,
    orgName: org.name,
    keyId: signer.key_id,
    chainStatus: chain?.status ?? "",
    rejectReason: chain?.reject_reason ?? null,
    details: "value" in details ? details.value : null,
    detailsError: "value" in details ? null : details.error,
  } satisfies Schema<"AuditAction">;
};

const audit = ({ db: state, params }: Ctx): Schema<"ProductAudit"> => {
  const key = typeof params.key === "string" ? params.key : "";
  const row = state.products.find((product) => product.qr_code === key || product.product_id === key);
  if (!row) throw productNotFound();
  return {
    productId: row.product_id,
    itemId: toHex(itemId(row.product_id), { size: 32 }),
    actions: state.warehouse_actions
      .filter((action) => action.submitted_at !== null && action.item_ids.includes(row.product_id))
      .sort((a, b) => a.submitted_at!.localeCompare(b.submitted_at!) || a.part_index - b.part_index)
      .flatMap((action) => {
        const view = actionJoined(state, action);
        return view ? [view] : [];
      }),
  };
};

export const historyHandlers = [
  route("get", "/analytics/summary", guards.adminOrOperator, summary),
  route("get", "/analytics/summary/", guards.adminOrOperator, summary),
  route("get", "/analytics/onchain", guards.adminOrOperator, onchain),
  route("get", "/analytics/onchain/", guards.adminOrOperator, onchain),
  route("get", "/analytics/throughput", guards.adminOrOperator, throughput),
  route("get", "/analytics/throughput/", guards.adminOrOperator, throughput),
  route("get", "/products/recent", guards.adminOrOperator, recent),
  route("get", "/products/recent/", guards.adminOrOperator, recent),
  route("get", "/products/timeline", guards.adminOrOperator, timeline),
  route("get", "/products/timeline/", guards.adminOrOperator, timeline),
  route("get", "/audit/products/:key", guards.auditorOrAdmin, audit),
];
