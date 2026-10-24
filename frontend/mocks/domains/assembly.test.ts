import { describe, expect, it } from "vitest";

import { toApiError } from "@/lib/api/errors";
import { api, unwrap, type Schema } from "@/lib/api/client";
import { getSession } from "@/lib/auth/session";
import { itemId } from "@/lib/signing/adr";
import { signAndSubmit, type SignedSubmit } from "@/lib/signing/sign-and-submit";

import { setChainMode } from "../chain";
import { db, saveDb } from "../db";
import { assemblyIds } from "../data/fixtures/assembly";
import { idGroup, mockId, seedIds, seededCredentialIds } from "../data/fixture-kit";
import { loginAs, setupMockApi, waitForChain } from "../testing";
import { setAuthenticatorMode } from "../virtual-authenticator";

setupMockApi();

const nilUuid = "00000000-0000-0000-0000-000000000000";
const shop9 = assemblyIds.destinations["SHOP-9"];
const shop7 = seedIds.destinations["SHOP-7"];
const p = assemblyIds.products;
const o = assemblyIds.orders;
const t = assemblyIds.tasks;
const unknownBin = mockId(idGroup.bin, 999999);
const unknownDestination = mockId(idGroup.destination, 999999);

const allocate = (destination_id: string) => unwrap(api.POST("/assembly/allocate", { body: { destination_id } }));
const tasksRaw = (query: Record<string, string>) => unwrap(api.GET("/assembly/tasks", { params: { query: query as never } }));
const tasksFor = (destination_id: string, extra: { operator_id?: string; status?: string } = {}) => tasksRaw({ destination_id, ...extra });
const pick = (product_id: string) => unwrap(api.POST("/assembly/pick", { body: { product_id } }));
const draft = (buffer_bin_id: string) => unwrap(api.POST("/assembly/scan-shipping-buffer/draft", { body: { buffer_bin_id } }));
const submit = (body: SignedSubmit) => unwrap(api.POST("/assembly/scan-shipping-buffer/submit", { body }));
const operationStatus = (operation_id: string) => unwrap(api.GET("/signed-actions/operations/{operation_id}", { params: { path: { operation_id } } }));
const destinations = (query: Record<string, string | number> = {}) => unwrap(api.GET("/destinations", { params: { query: query as never } }));
const destinationsSlash = async (query = "") => {
  const response = await fetch(`${location.origin}/api/destinations/${query}`, {
    headers: { Authorization: `Bearer ${getSession()?.access_token}` },
  });
  const body = (await response.json()) as { data: Schema<"DestinationListResponse">; error: { code: string; message: string } | null };
  if (!response.ok) throw toApiError(response.status, body.error);
  return body.data;
};

describe("аллокация", () => {
  it("резервирует NEW-заказы целиком, нехватка — в insufficient_orders без частичного заслуживания", async () => {
    await loginAs("operator");
    const result = await allocate(shop9);
    expect(result).toEqual({
      allocated_orders: 2,
      allocated_products: 3,
      insufficient_orders: [{ order_id: o["ORD-ASM-002"], missing_skus: [{ sku_id: seedIds.skus.puma, sku_name: "Рюкзак Puma Core", missing_qty: 4 }] }],
    });
    const state = db();
    const product = (id: string) => state.products.find((row) => row.product_id === id);
    expect(product(mockId(idGroup.product, 3))).toMatchObject({ status: "ALLOCATED", order_id: o["ORD-ASM-001"] });
    expect(product(p["QR-ASM-0001"])).toMatchObject({ status: "ALLOCATED", order_id: o["ORD-ASM-001"] });
    expect(product(p["QR-ASM-0002"])).toMatchObject({ status: "STORED", order_id: null });
    expect(product(mockId(idGroup.product, 14))).toMatchObject({ status: "ALLOCATED", order_id: o["ORD-ASM-003"] });
    const order = (id: string) => state.orders.find((row) => row.order_id === id)?.status;
    expect(order(o["ORD-ASM-001"])).toBe("ALLOCATED");
    expect(order(o["ORD-ASM-003"])).toBe("ALLOCATED");
    expect(order(o["ORD-ASM-002"])).toBe("NEW");
    expect(order(o["ORD-ASM-010"])).toBe("ALLOCATED");
  });

  it("admin может аллоцировать, auditor — 403", async () => {
    await loginAs("admin");
    await expect(allocate(shop9)).resolves.toEqual({ allocated_orders: 2, allocated_products: 3, insufficient_orders: [expect.anything()] });
    await loginAs("auditor");
    await expect(allocate(shop9)).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Требуется роль администратора или оператора" });
  });

  it("неизвестный магазин — пустой успех; не uuid/nil-uuid/лишнее поле — ошибки", async () => {
    await loginAs("operator");
    await expect(allocate(unknownDestination)).resolves.toEqual({ allocated_orders: 0, allocated_products: 0, insufficient_orders: [] });
    await expect(allocate("x")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "destination_id не uuid" });
    await expect(allocate(nilUuid)).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "Невалидные входные данные" });
    await expect(unwrap(api.POST("/assembly/allocate", { body: { destination_id: shop9, extra: 1 } as never }))).rejects.toMatchObject({
      status: 400,
      code: "INVALID_REQUEST",
      message: "Невалидный JSON в теле запроса",
    });
  });
});

describe("задачи сборки", () => {
  it("после allocate: задачи по section/code ячейки, задачи ORD-ASM-010 на месте", async () => {
    await loginAs("operator");
    await allocate(shop9);
    const result = await tasksFor(shop9);
    expect(result.tasks.map((task) => task.qr_code)).toEqual(["QR-2026-0003", "QR-ASM-0030", "QR-ASM-0001", "QR-2026-0014", "QR-ASM-0010", "QR-ASM-0011"]);
    expect(result.tasks.find((task) => task.product_id === p["QR-ASM-0010"])).toMatchObject({
      order_no: "ORD-ASM-010",
      from_bin_code: "A-02-01",
      from_bin_section: "A",
      sku_name: "Кроссовки Nike Air Max 90",
    });
    expect(result.tasks.find((task) => task.product_id === p["QR-ASM-0011"])).toMatchObject({
      order_no: "ORD-ASM-010",
      from_bin_code: "B-01-02",
      from_bin_section: "B",
      sku_name: "Футболка Adidas Originals",
    });
  });

  it("валидация запроса: destination_id → operator_id → status, admin — 403", async () => {
    await loginAs("operator");
    await expect(tasksRaw({})).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "destination_id обязателен" });
    await expect(tasksRaw({ destination_id: "abc" })).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "destination_id не uuid" });
    await expect(tasksRaw({ destination_id: shop9, operator_id: "abc" })).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "operator_id не uuid" });
    await expect(tasksRaw({ destination_id: shop9, status: "WRONG" })).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "неизвестный статус задачи" });
    await loginAs("admin");
    await expect(tasksFor(shop9)).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Только оператор может выполнять это действие" });
  });

  it("SHOP-7 DONE — ровно задача QR-ASM-0020; фильтр чужого оператора пуст", async () => {
    await loginAs("operator");
    const result = await tasksFor(shop7, { status: "DONE" });
    expect(result.tasks).toEqual([
      { task_id: t["QR-ASM-0020"], product_id: p["QR-ASM-0020"], qr_code: "QR-ASM-0020", sku_name: "Кроссовки Nike Air Max 90", from_bin_code: "A-01-01", from_bin_section: "A", order_no: "ORD-ASM-020" },
    ]);
    const other = await tasksFor(shop7, { operator_id: seedIds.users.operator2, status: "DONE" });
    expect(other.tasks).toEqual([]);
  });
});

describe("подбор", () => {
  it("pick QR-ASM-0010 — задача DONE, товар ASSEMBLED, cart_size 2", async () => {
    await loginAs("operator");
    await expect(pick(p["QR-ASM-0010"])).resolves.toEqual({ product_id: p["QR-ASM-0010"], cart_size: 2 });
    const state = db();
    expect(state.assembly_tasks.find((row) => row.event_id === t["QR-ASM-0010"])).toMatchObject({ status: "DONE", operator_id: seedIds.users.operator });
    expect(state.products.find((row) => row.product_id === p["QR-ASM-0010"])?.status).toBe("ASSEMBLED");
  });

  it("повторный pick и pick без задачи — NO_TASK_FOR_PRODUCT", async () => {
    await loginAs("operator");
    await pick(p["QR-ASM-0010"]);
    await expect(pick(p["QR-ASM-0010"])).rejects.toMatchObject({ status: 409, code: "NO_TASK_FOR_PRODUCT", message: "Нет задачи сборки для этого товара" });
    await expect(pick(mockId(idGroup.product, 9))).rejects.toMatchObject({ status: 409, code: "NO_TASK_FOR_PRODUCT", message: "Нет задачи сборки для этого товара" });
  });

  it("FAILED раскладка в цепи — CHAIN_EVENT_REJECTED, задача остаётся PENDING", async () => {
    await loginAs("operator");
    await expect(pick(p["QR-ASM-0030"])).rejects.toMatchObject({
      status: 409,
      code: "CHAIN_EVENT_REJECTED",
      message: "Подбор отклонён: размещение товара не подтверждено в блокчейне (FAILED)",
    });
    expect(db().assembly_tasks.find((row) => row.event_id === t["QR-ASM-0030"])?.status).toBe("PENDING");
  });

  it("pick: product_id не uuid / nil-uuid", async () => {
    await loginAs("operator");
    await expect(pick("x")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "product_id не uuid" });
    await expect(pick(nilUuid)).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "Невалидные входные данные" });
  });

  it("задача PENDING, но товар не ALLOCATED — PRODUCT_NOT_ALLOCATED", async () => {
    await loginAs("operator");
    const state = db();
    const row = state.products.find((product) => product.product_id === p["QR-ASM-0011"]);
    if (row) row.status = "STORED";
    saveDb();
    await expect(pick(p["QR-ASM-0011"])).rejects.toMatchObject({ status: 409, code: "PRODUCT_NOT_ALLOCATED", message: "Товар не в статусе ALLOCATED" });
  });
});

describe("выгрузка корзины в буфер отгрузки", () => {
  it("draft → submit SHOP-7: товар в буфере, заказ ASSEMBLED, задача отмечена, SENT → APPLIED", async () => {
    await loginAs("operator");
    const draftOp = await draft(seedIds.bins["BIN-SHIP-7"]);
    expect(draftOp.action_type).toBe(3);
    expect(draftOp.parts).toHaveLength(1);
    expect(draftOp.parts[0].item_ids).toEqual([p["QR-ASM-0020"]]);
    const result = await signAndSubmit(draftOp, submit);
    expect(result).toMatchObject({ buffer_bin_id: seedIds.bins["BIN-SHIP-7"], products_placed: 1, orders_assembled: 1 });
    const state = db();
    expect(state.products.find((row) => row.product_id === p["QR-ASM-0020"])).toMatchObject({ status: "READY_TO_SHIP", bin_id: seedIds.bins["BIN-SHIP-7"] });
    expect(state.orders.find((row) => row.order_id === o["ORD-ASM-020"])?.status).toBe("ASSEMBLED");
    const task = state.assembly_tasks.find((row) => row.event_id === t["QR-ASM-0020"]);
    expect(task).toMatchObject({ action_id: draftOp.parts[0].action_id });
    const part = state.warehouse_actions.find((row) => row.action_id === draftOp.parts[0].action_id);
    expect(task?.unloaded_at).toEqual(new Date(part!.details.occurred_at_ms as number).toISOString());
    expect((await operationStatus(result.operation_id)).parts.map((partState) => partState.state)).toEqual(["SENT"]);
    await waitForChain();
    expect((await operationStatus(result.operation_id)).parts).toEqual([expect.objectContaining({ state: "APPLIED", reject_reason: null })]);
    await expect(draft(seedIds.bins["BIN-SHIP-7"])).rejects.toMatchObject({ status: 409, code: "CART_EMPTY", message: "Корзина оператора пуста" });
  });

  it("два товара в BIN-SHIP-9 — заказ ORD-ASM-010 собран", async () => {
    await loginAs("operator");
    await pick(p["QR-ASM-0010"]);
    await pick(p["QR-ASM-0011"]);
    const draftOp = await draft(assemblyIds.bins["BIN-SHIP-9"]);
    const expected = [p["QR-ASM-0010"], p["QR-ASM-0011"]].sort((a, b) => (itemId(a) < itemId(b) ? -1 : 1));
    expect(draftOp.parts).toHaveLength(1);
    expect(draftOp.parts[0].item_ids).toEqual(expected);
    const result = await signAndSubmit(draftOp, submit);
    expect(result).toMatchObject({ products_placed: 2, orders_assembled: 1, buffer_bin_id: assemblyIds.bins["BIN-SHIP-9"] });
    expect(db().orders.find((row) => row.order_id === o["ORD-ASM-010"])?.status).toBe("ASSEMBLED");
  });

  it("частичная выгрузка — заказ остаётся ALLOCATED", async () => {
    await loginAs("operator");
    await pick(p["QR-ASM-0010"]);
    const result = await signAndSubmit(await draft(assemblyIds.bins["BIN-SHIP-9"]), submit);
    expect(result).toMatchObject({ products_placed: 1, orders_assembled: 0 });
    const state = db();
    expect(state.orders.find((row) => row.order_id === o["ORD-ASM-010"])?.status).toBe("ALLOCATED");
    expect(state.products.find((row) => row.product_id === p["QR-ASM-0011"])?.status).toBe("ALLOCATED");
  });
});

describe("границы черновика", () => {
  it("чужой магазин в корзине — DESTINATION_MISMATCH; у пустого оператора — CART_EMPTY", async () => {
    await loginAs("operator");
    await expect(draft(assemblyIds.bins["BIN-SHIP-9"])).rejects.toMatchObject({
      status: 409,
      code: "DESTINATION_MISMATCH",
      message: "Буфер отгрузки другого магазина: собранные товары не для этого пункта назначения",
    });
    await loginAs("operator2");
    await expect(draft(assemblyIds.bins["BIN-SHIP-9"])).rejects.toMatchObject({ status: 409, code: "CART_EMPTY", message: "Корзина оператора пуста" });
  });

  it("ячейка не буфер отгрузки / без магазина / не uuid", async () => {
    await loginAs("operator");
    const binNotBuffer = { status: 400, code: "BIN_NOT_SHIPPING_BUFFER", message: "Ячейка не является буфером отгрузки" };
    await expect(draft(seedIds.bins["A-01-01"])).rejects.toMatchObject(binNotBuffer);
    await expect(draft(seedIds.bins["BUFFER-01"])).rejects.toMatchObject(binNotBuffer);
    await expect(draft(unknownBin)).rejects.toMatchObject(binNotBuffer);
    await expect(draft(assemblyIds.bins["BIN-SHIP-0"])).rejects.toMatchObject({ status: 404, code: "DESTINATION_NOT_FOUND", message: "магазин не найден" });
    await expect(draft("x")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "buffer_bin_id должен быть UUID" });
  });

  it("отозванный ключ оператора — SIGNER_KEY_NOT_ACTIVE", async () => {
    await loginAs("operator");
    const state = db();
    const key = state.passkey_credentials.find((row) => row.credential_id === seededCredentialIds.operator);
    if (key) key.status = "REVOKED";
    saveDb();
    await expect(draft(seedIds.bins["BIN-SHIP-7"])).rejects.toMatchObject({ status: 409, code: "SIGNER_KEY_NOT_ACTIVE", message: "Ключ оператора не активен в реестре" });
  });
});

describe("подпись и отправка выгрузки", () => {
  it("состав корзины изменился после черновика — DRAFT_STALE, товары остаются ASSEMBLED", async () => {
    await loginAs("operator");
    await pick(p["QR-ASM-0010"]);
    const stale = await draft(assemblyIds.bins["BIN-SHIP-9"]);
    await pick(p["QR-ASM-0011"]);
    await expect(signAndSubmit(stale, submit)).rejects.toMatchObject({ status: 409, code: "DRAFT_STALE", message: "Состав корзины изменился, подтвердите выгрузку заново" });
    const state = db();
    expect(state.products.find((row) => row.product_id === p["QR-ASM-0010"])?.status).toBe("ASSEMBLED");
    expect(state.products.find((row) => row.product_id === p["QR-ASM-0011"])?.status).toBe("ASSEMBLED");
  });

  it("повторная отправка — OPERATION_ALREADY_SUBMITTED; corrupt-подпись — INVALID_ASSERTION без записи", async () => {
    await loginAs("operator");
    await pick(p["QR-ASM-0010"]);
    const draftOp = await draft(assemblyIds.bins["BIN-SHIP-9"]);
    let body: SignedSubmit | undefined;
    await signAndSubmit(draftOp, (signed) => {
      body = signed;
      return submit(signed);
    });
    await expect(submit(body!)).rejects.toMatchObject({ status: 409, code: "OPERATION_ALREADY_SUBMITTED" });

    await pick(p["QR-ASM-0011"]);
    setAuthenticatorMode("corrupt");
    await expect(signAndSubmit(await draft(assemblyIds.bins["BIN-SHIP-9"]), submit)).rejects.toMatchObject({
      status: 400,
      code: "INVALID_ASSERTION",
      message: "Подтверждение не прошло проверку",
    });
    const state = db();
    expect(state.products.find((row) => row.product_id === p["QR-ASM-0011"])?.status).toBe("ASSEMBLED");
    expect(state.assembly_tasks.find((row) => row.event_id === t["QR-ASM-0011"])?.action_id).toBeNull();
  });

  it("цепь отклонила — REJECTED с reject_reason 5", async () => {
    await loginAs("operator");
    setChainMode("reject:5");
    await pick(p["QR-ASM-0010"]);
    const result = await signAndSubmit(await draft(assemblyIds.bins["BIN-SHIP-9"]), submit);
    await waitForChain();
    expect((await operationStatus(result.operation_id)).parts).toEqual([expect.objectContaining({ state: "REJECTED", reject_reason: 5 })]);
  });
});

describe("справочник магазинов", () => {
  it("список по code, оба пути, фильтр warehouse_id и его ошибки, RBAC", async () => {
    await loginAs("operator");
    const result = await destinations();
    expect(result.destinations.map((row) => row.code)).toEqual(["SHOP-5", "SHOP-7", "SHOP-9"]);
    expect(result.destinations.find((row) => row.code === "SHOP-9")).toEqual({
      destination_id: shop9,
      code: "SHOP-9",
      name: "Магазин №9 Тверская",
      address: "г. Москва, ул. Тверская, 9",
      warehouse_id: 1,
    });
    const viaSlash = await destinationsSlash();
    expect(viaSlash.destinations.map((row) => row.code)).toEqual(["SHOP-5", "SHOP-7", "SHOP-9"]);
    await expect(destinations({ warehouse_id: 2 })).resolves.toEqual({ destinations: [] });
    await expect(destinations({ warehouse_id: "abc" })).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "warehouse_id должен быть числом" });
    await expect(destinations({ warehouse_id: 0 })).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "warehouse_id должен быть положительным числом" });
    await loginAs("admin");
    await expect(destinations()).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Только оператор может выполнять это действие" });
  });
});
