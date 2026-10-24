import { describe, expect, it } from "vitest";

import { api, unwrap } from "@/lib/api/client";
import { itemId } from "@/lib/signing/adr";
import { signAndSubmit, type SignedSubmit } from "@/lib/signing/sign-and-submit";

import { setChainMode } from "../chain";
import { db } from "../db";
import { shippingIds } from "../data/fixtures/shipping";
import { ago, idGroup, later, mockId, seedIds, seededCredentialIds } from "../data/fixture-kit";
import { loginAs, setupMockApi, waitForChain } from "../testing";
import { setAuthenticatorMode } from "../virtual-authenticator";

setupMockApi();

const shipBin = seedIds.bins["BIN-SHIP-5"];
const shopBin = shippingIds.bins["BIN-SHIP-5B"];
const bin7 = seedIds.bins["BIN-SHIP-7"];
const dsp = shippingIds.dispatches;
const orderIds = shippingIds.orders;
const products = shippingIds.products;
const seedDispatch = seedIds.dispatches["DSP-2026-0421-001"];
const unknownBin = mockId(idGroup.bin, 999999);
const unknownDispatch = mockId(idGroup.dispatch, 999999);
const unknownDestination = mockId(idGroup.destination, 999999);
const storedSeedProduct = mockId(idGroup.product, 9);

const shipBinRow = () => db().outbound_dispatches.find((row) => row.dispatch_id === dsp["DSP-SHP-001"]);
const productStatus = (qr: keyof typeof products) => db().products.find((row) => row.qr_code === qr)?.status;
const orderStatus = (orderId: string) => db().orders.find((row) => row.order_id === orderId)?.status;
const operationStatus = (operation_id: string) => unwrap(api.GET("/signed-actions/operations/{operation_id}", { params: { path: { operation_id } } }));

const scanBuffer = (buffer_bin_id: string) => unwrap(api.POST("/shipping/scan-buffer", { body: { buffer_bin_id } }));
const scanDriver = (dispatch_code: string) => unwrap(api.POST("/shipping/scan-driver", { body: { dispatch_code } }));
const draft = (bufferBinId: string, dispatchId: string, productIds?: string[]) => {
  const body = { buffer_bin_id: bufferBinId, dispatch_id: dispatchId, ...(productIds ? { product_ids: productIds } : {}) };
  return unwrap(api.POST("/shipping/ship/draft", { body }));
};
const submit = (body: SignedSubmit) => unwrap(api.POST("/shipping/ship/submit", { body }));
const ship = async (bufferBinId: string, dispatchId: string, productIds?: string[]) => {
  const operation = await draft(bufferBinId, dispatchId, productIds);
  return signAndSubmit(operation, submit);
};
const listDispatches = (query?: Record<string, unknown>) => unwrap(api.GET("/dispatches/", { params: { query: query as never } }));
const dispatchById = (dispatch_id: string) =>
  unwrap(api.GET("/dispatches/{dispatch_id}", { params: { path: { dispatch_id } } }));
const createDispatch = (body: Record<string, unknown>) => unwrap(api.POST("/dispatches/", { body: body as never }));

describe("сканирование буфера отгрузки", () => {
  it("отдаёт READY_TO_SHIP товары ячейки по времени создания", async () => {
    await loginAs("operator");
    const result = await scanBuffer(shipBin);
    expect(result.buffer_bin).toEqual({
      id: shipBin,
      code: "BIN-SHIP-5",
      destination: { id: seedIds.destinations["SHOP-5"], code: "SHOP-5", name: "Магазин №5 Маросейка" },
    });
    expect(result.products.map((product) => product.qr_code)).toEqual(["QR-SHP-0001", "QR-SHP-0002", "QR-SHP-0003"]);
    expect(result.count).toBe(3);
    expect(result.products[0]).toEqual({
      product_id: products["QR-SHP-0001"],
      qr_code: "QR-SHP-0001",
      sku_name: "Кроссовки Nike Air Max 90",
      order_external_no: "ORD-SHP-001",
    });
  });

  it.each([
    ["ячейка хранения", seedIds.bins["A-01-01"]],
    ["буфер приёмки", seedIds.bins["BUFFER-01"]],
  ])("%s — BIN_NOT_SHIPPING_BUFFER", async (_, binId) => {
    await loginAs("operator");
    await expect(scanBuffer(binId)).rejects.toMatchObject({
      status: 400,
      code: "BIN_NOT_SHIPPING_BUFFER",
      message: "Ячейка не является буфером отгрузки",
    });
  });

  it("неизвестная ячейка — 404 BIN_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(scanBuffer(unknownBin)).rejects.toMatchObject({ status: 404, code: "BIN_NOT_FOUND", message: "Ячейка не найдена" });
  });

  it("буфер не UUID — INVALID_REQUEST с именем поля", async () => {
    await loginAs("operator");
    await expect(scanBuffer("x")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "buffer_bin_id должен быть UUID" });
  });

  it("лишнее поле в теле — INVALID_REQUEST", async () => {
    await loginAs("operator");
    const response = unwrap(api.POST("/shipping/scan-buffer", { body: { buffer_bin_id: shipBin, extra: 1 } as never }));
    await expect(response).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "Невалидный JSON в теле запроса" });
  });

  it("админ — FORBIDDEN, отгрузка только для оператора", async () => {
    await loginAs("admin");
    await expect(scanBuffer(shipBin)).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Только оператор может выполнять это действие" });
  });
});

describe("сканирование водителя", () => {
  it("SCHEDULED → AT_GATE с arrived_at, повторный скан идемпотентен", async () => {
    await loginAs("operator");
    const first = await scanDriver("DSP-SHP-005");
    expect(first).toEqual({
      dispatch_id: dsp["DSP-SHP-005"],
      dispatch_code: "DSP-SHP-005",
      vehicle_number: "B555EE599",
      driver_name: "Сергей Морозов",
      driver_phone: null,
      destination: { id: seedIds.destinations["SHOP-7"], code: "SHOP-7", name: "Магазин №7 Петровка" },
      status: "AT_GATE",
      arrived_at: expect.any(String),
    });
    const second = await scanDriver("DSP-SHP-005");
    expect(second.status).toBe("AT_GATE");
    expect(second.arrived_at).toBe(first.arrived_at);
  });

  it.each([
    ["DEPARTED", "DSP-SHP-003", 409, "DISPATCH_ALREADY_DEPARTED", "Рейс уже отправлен"],
    ["CANCELLED", "DSP-SHP-004", 409, "DISPATCH_CANCELLED", "Рейс отменён"],
    ["неизвестный", "DSP-НЕТ", 404, "DISPATCH_NOT_FOUND", "Рейс не найден"],
    ["пустой код", "", 400, "INVALID_REQUEST", "Невалидные входные данные"],
  ])("скан водителя %s — %s %s", async (_, code, httpStatus, errCode, message) => {
    await loginAs("operator");
    await expect(scanDriver(code)).rejects.toMatchObject({ status: httpStatus, code: errCode, message });
  });

  it("уже AT_GATE — возвращается как есть, arrived_at не меняется", async () => {
    await loginAs("operator");
    const before = (await dispatchById(dsp["DSP-SHP-001"])).dispatch;
    const result = await scanDriver("DSP-SHP-001");
    expect(result.status).toBe("AT_GATE");
    expect(result.arrived_at).toBe(before.arrived_at);
  });
});

describe("черновик и отправка отгрузки", () => {
  it("bulk: три товара одной частью, заказы SHIPPED, рейс у ворот; второй буфер довозит до DEPARTED", async () => {
    await loginAs("operator");
    const operation = await draft(shipBin, dsp["DSP-SHP-001"]);
    expect(operation.action_type).toBe(4);
    expect(operation.parts).toHaveLength(1);
    const expectedItems = [products["QR-SHP-0001"], products["QR-SHP-0002"], products["QR-SHP-0003"]].sort((a, b) => (itemId(a) < itemId(b) ? -1 : 1));
    expect(operation.parts[0].item_ids).toEqual(expectedItems);
    expect(operation.allow_credentials).toEqual([{ type: "public-key", id: seededCredentialIds.operator }]);
    const shippingsBefore = db().shippings.length;
    const result = await signAndSubmit(operation, submit);
    expect(result).toEqual({
      products_shipped: 3,
      outbox_events_created: 1,
      orders_completed: 2,
      orders_partially_shipped: 0,
      dispatch_departed: false,
      buffer_remaining: 1,
      operation_id: operation.operation_id,
    });
    expect(productStatus("QR-SHP-0001")).toBe("SHIPPED");
    expect(productStatus("QR-SHP-0002")).toBe("SHIPPED");
    expect(productStatus("QR-SHP-0003")).toBe("SHIPPED");
    expect(orderStatus(orderIds["ORD-SHP-001"])).toBe("SHIPPED");
    expect(orderStatus(orderIds["ORD-SHP-002"])).toBe("SHIPPED");
    const written = db().shippings.slice(shippingsBefore);
    expect(written).toHaveLength(3);
    for (const row of written) {
      expect(row.action_id).toBe(operation.parts[0].action_id);
      expect(row.dispatch_id).toBe(dsp["DSP-SHP-001"]);
    }

    const second = await ship(shopBin, dsp["DSP-SHP-001"]);
    expect(second).toEqual({
      products_shipped: 1,
      outbox_events_created: 1,
      orders_completed: 1,
      orders_partially_shipped: 0,
      dispatch_departed: true,
      buffer_remaining: 0,
      operation_id: expect.any(String),
    });
    expect(shipBinRow()).toMatchObject({ status: "DEPARTED" });
    expect(shipBinRow()?.departed_at).not.toBeNull();
    await expect(draft(shipBin, dsp["DSP-SHP-001"])).rejects.toMatchObject({
      status: 409,
      code: "DISPATCH_NOT_AT_GATE",
      message: "Рейс не находится у ворот",
    });
  });

  it("spot: один товар, заказ частично отгружен", async () => {
    await loginAs("operator");
    const result = await ship(shipBin, dsp["DSP-SHP-001"], [products["QR-SHP-0001"]]);
    expect(result).toEqual({
      products_shipped: 1,
      outbox_events_created: 1,
      orders_completed: 0,
      orders_partially_shipped: 1,
      dispatch_departed: false,
      buffer_remaining: 3,
      operation_id: expect.any(String),
    });
    expect(orderStatus(orderIds["ORD-SHP-001"])).toBe("PARTIALLY_SHIPPED");
    expect(shipBinRow()?.status).toBe("AT_GATE");
  });

  it.each([
    ["отменённый рейс", dsp["DSP-SHP-004"]],
    ["seed рейс SCHEDULED", seedDispatch],
  ])("bulk на %s — DISPATCH_NOT_AT_GATE", async (_, dispatchId) => {
    await loginAs("operator");
    await expect(draft(shipBin, dispatchId)).rejects.toMatchObject({ status: 409, code: "DISPATCH_NOT_AT_GATE", message: "Рейс не находится у ворот" });
  });

  it("поездка не на ту зону — DESTINATION_MISMATCH", async () => {
    await loginAs("operator");
    await expect(draft(shipBin, dsp["DSP-SHP-002"])).rejects.toMatchObject({ status: 409, code: "DESTINATION_MISMATCH", message: "Рейс назначен на другую зону" });
  });

  it("неизвестный рейс — 404 DISPATCH_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(draft(shipBin, unknownDispatch)).rejects.toMatchObject({ status: 404, code: "DISPATCH_NOT_FOUND", message: "Рейс не найден" });
  });

  it.each([
    ["seed-товар STORED не в буфере", [storedSeedProduct]],
    ["дубликат товара", [products["QR-SHP-0001"], products["QR-SHP-0001"]]],
    ["товар из другого буфера", [products["QR-SHP-0007"]]],
  ])("spot %s — PRODUCT_NOT_IN_BUFFER", async (_, productIds) => {
    await loginAs("operator");
    await expect(draft(shipBin, dsp["DSP-SHP-001"], productIds)).rejects.toMatchObject({
      status: 409,
      code: "PRODUCT_NOT_IN_BUFFER",
      message: "Товар не найден в буфере или не готов к отгрузке",
    });
  });

  it("сборка не подтверждена: bulk — PICKING_NOT_CONFIRMED ровно с одним товаром, spot с подтверждённым проходит", async () => {
    await loginAs("operator");
    await expect(draft(bin7, dsp["DSP-SHP-002"])).rejects.toMatchObject({
      status: 409,
      code: "PICKING_NOT_CONFIRMED",
      message: "Сборка части товаров (1 шт.) ещё не подтверждена в цепи. Повторите отгрузку чуть позже",
    });
    const result = await ship(bin7, dsp["DSP-SHP-002"], [products["QR-SHP-0006"]]);
    expect(result).toEqual({
      products_shipped: 1,
      outbox_events_created: 1,
      orders_completed: 0,
      orders_partially_shipped: 1,
      dispatch_departed: false,
      buffer_remaining: 1,
      operation_id: expect.any(String),
    });
    expect(orderStatus(orderIds["ORD-SHP-005"])).toBe("PARTIALLY_SHIPPED");
  });

  it("буфер опустел после spot — дальнейший bulk даёт BUFFER_EMPTY, рейс ещё у ворот", async () => {
    await loginAs("operator");
    await ship(shopBin, dsp["DSP-SHP-001"], [products["QR-SHP-0007"]]);
    await expect(draft(shopBin, dsp["DSP-SHP-001"])).rejects.toMatchObject({ status: 409, code: "BUFFER_EMPTY", message: "Буфер отгрузки пуст" });
    expect(shipBinRow()?.status).toBe("AT_GATE");
  });
});

describe("валидация параметров черновика", () => {
  it("dispatch_id не UUID — INVALID_REQUEST", async () => {
    await loginAs("operator");
    await expect(draft(shipBin, "x")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "dispatch_id должен быть UUID" });
  });

  it("оба поля не UUID — сообщение про buffer_bin_id", async () => {
    await loginAs("operator");
    await expect(draft("x", "x")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "buffer_bin_id должен быть UUID" });
  });

  it("product_ids не UUID — INVALID_REQUEST", async () => {
    await loginAs("operator");
    await expect(draft(shipBin, dsp["DSP-SHP-001"], ["x"])).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "product_ids должен содержать только UUID" });
  });

  it("product_ids длиннее 200 — INVALID_REQUEST", async () => {
    await loginAs("operator");
    await expect(draft(shipBin, dsp["DSP-SHP-001"], Array.from({ length: 201 }, () => products["QR-SHP-0001"]))).rejects.toMatchObject({
      status: 400,
      code: "INVALID_REQUEST",
      message: "product_ids не может содержать более 200 элементов",
    });
  });

  it("nil-UUID рейса — INVALID_REQUEST", async () => {
    await loginAs("operator");
    await expect(draft(shipBin, "00000000-0000-0000-0000-000000000000")).rejects.toMatchObject({
      status: 400,
      code: "INVALID_REQUEST",
      message: "Невалидные входные данные",
    });
  });

  it("у оператора нет ключа — SIGNER_KEY_NOT_ACTIVE", async () => {
    await loginAs("operator2");
    await expect(draft(shipBin, dsp["DSP-SHP-001"])).rejects.toMatchObject({ status: 409, code: "SIGNER_KEY_NOT_ACTIVE", message: "Ключ оператора не активен в реестре" });
  });
});

describe("подпись и повторная отправка", () => {
  it("состав отгрузки устарел — DRAFT_STALE", async () => {
    await loginAs("operator");
    const stale = await draft(shipBin, dsp["DSP-SHP-001"]);
    await ship(shipBin, dsp["DSP-SHP-001"], [products["QR-SHP-0001"]]);
    await expect(signAndSubmit(stale, submit)).rejects.toMatchObject({
      status: 409,
      code: "DRAFT_STALE",
      message: "Состав отгрузки изменился, подтвердите отгрузку заново",
    });
    expect(productStatus("QR-SHP-0002")).toBe("READY_TO_SHIP");
    expect(productStatus("QR-SHP-0003")).toBe("READY_TO_SHIP");
  });

  it("повторная отправка — OPERATION_ALREADY_SUBMITTED", async () => {
    await loginAs("operator");
    const operation = await draft(shipBin, dsp["DSP-SHP-001"]);
    let body: SignedSubmit | undefined;
    await signAndSubmit(operation, (signed) => {
      body = signed;
      return submit(signed);
    });
    await expect(submit(body!)).rejects.toMatchObject({ status: 409, code: "OPERATION_ALREADY_SUBMITTED" });
  });

  it("битая подпись — INVALID_ASSERTION, товары остаются в буфере", async () => {
    await loginAs("operator");
    setAuthenticatorMode("corrupt");
    await expect(signAndSubmit(await draft(shipBin, dsp["DSP-SHP-001"]), submit)).rejects.toMatchObject({
      status: 400,
      code: "INVALID_ASSERTION",
      message: "Подтверждение не прошло проверку",
    });
    expect((await scanBuffer(shipBin)).count).toBe(3);
  });

  it("цепь отклонила Ship — SENT, затем REJECTED с кодом причины", async () => {
    await loginAs("operator");
    setChainMode("reject:9");
    const operation = await draft(shipBin, dsp["DSP-SHP-001"]);
    const result = await signAndSubmit(operation, submit);
    expect(result.products_shipped).toBe(3);
    expect((await operationStatus(operation.operation_id)).parts.map((part) => part.state)).toEqual(["SENT"]);
    await waitForChain();
    expect((await operationStatus(operation.operation_id)).parts).toEqual([expect.objectContaining({ state: "REJECTED", reject_reason: 9 })]);
  });
});

describe("реестр рейсов", () => {
  it("список содержит seed и фикстуры по scheduled_at, фильтры работают", async () => {
    await loginAs("operator");
    expect((await listDispatches()).map((row) => row.dispatch_code)).toEqual([
      "DSP-SHP-003",
      "DSP-SHP-001",
      "DSP-SHP-002",
      "DSP-SHP-004",
      "DSP-SHP-005",
      "DSP-2026-0421-001",
    ]);
    expect((await listDispatches({ status: "AT_GATE" })).map((row) => row.dispatch_code)).toEqual(["DSP-SHP-001", "DSP-SHP-002"]);
    expect((await listDispatches({ destination_id: seedIds.destinations["SHOP-7"] })).map((row) => row.dispatch_code)).toEqual(["DSP-SHP-002", "DSP-SHP-005"]);
    expect(await listDispatches({ warehouse_id: 2 })).toEqual([]);
  });

  it.each([
    ["warehouse_id не число", { warehouse_id: "abc" }, `Invalid request: strconv.Atoi: parsing "abc": invalid syntax`],
    ["destination_id не UUID", { destination_id: "x" }, "Invalid request: invalid UUID format"],
  ])("список: %s — INVALID_REQUEST", async (_, query, message) => {
    await loginAs("operator");
    await expect(listDispatches(query)).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message });
  });

  it("админ список не смотрит — FORBIDDEN", async () => {
    await loginAs("admin");
    await expect(listDispatches()).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Только оператор может выполнять это действие" });
  });

  it("рейс по id", async () => {
    await loginAs("operator");
    const result = await dispatchById(dsp["DSP-SHP-003"]);
    expect(result.dispatch.dispatch_code).toBe("DSP-SHP-003");
    expect(result.dispatch.status).toBe("DEPARTED");
    expect(result.dispatch.departed_at).not.toBeNull();
  });

  it("неизвестный рейс — 404 NOT_FOUND/DISPATCH_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(dispatchById(unknownDispatch)).rejects.toMatchObject({ status: 404, code: "NOT_FOUND", message: "DISPATCH_NOT_FOUND" });
  });

  it("id не UUID — INVALID_REQUEST", async () => {
    await loginAs("operator");
    await expect(dispatchById("x")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "invalid dispatch_id format: invalid UUID format" });
  });

  it("создание: код DSP-<UTC-дата>-<NNN> по счётчику за сегодня, рейс доступен по id", async () => {
    await loginAs("operator");
    const iso = new Date().toISOString();
    const dayStart = Date.parse(iso.slice(0, 10));
    const seq = db().outbound_dispatches.filter((row) => Date.parse(row.created_at) >= dayStart).length + 1;
    const expected = `DSP-${iso.slice(0, 4)}-${iso.slice(5, 7)}${iso.slice(8, 10)}-${String(seq).padStart(3, "0")}`;
    const created = await createDispatch({
      destination_id: seedIds.destinations["SHOP-7"],
      vehicle_number: "B777OP77",
      driver_name: "Пётр Иванов",
      scheduled_at: later(60),
    });
    expect(created.dispatch.dispatch_code).toBe(expected);
    expect(created.dispatch.status).toBe("SCHEDULED");
    expect(created.dispatch.warehouse_id).toBe(1);
    expect(created.dispatch.driver_phone).toBeNull();
    const found = await dispatchById(created.dispatch.dispatch_id);
    expect(found.dispatch.dispatch_code).toBe(expected);
  });

  it.each([
    ["driver_name пустой", { vehicle_number: "B777OP77", driver_name: "", scheduled_at: later(60) }, "Invalid request body: you need to pass a driver_name"],
    ["driver_name отсутствует", { vehicle_number: "B777OP77", scheduled_at: later(60) }, "Invalid request body: you need to pass a driver_name"],
    ["vehicle_number пустой", { vehicle_number: "", driver_name: "Пётр Иванов", scheduled_at: later(60) }, "Invalid request body: you need to pass a vehicle_number"],
    ["vehicle_number отсутствует", { driver_name: "Пётр Иванов", scheduled_at: later(60) }, "Invalid request body: you need to pass a vehicle_number"],
    ["scheduled_at в прошлом", { vehicle_number: "B777OP77", driver_name: "Пётр Иванов", scheduled_at: ago(60) }, "Invalid request body: scheduled_at should not be past time"],
    ["scheduled_at отсутствует", { vehicle_number: "B777OP77", driver_name: "Пётр Иванов" }, "Invalid request body: scheduled_at should not be past time"],
    ["scheduled_at не разбирается", { vehicle_number: "B777OP77", driver_name: "Пётр Иванов", scheduled_at: "завтра" }, "Invalid request body: decode json"],
    ["лишнее поле", { vehicle_number: "B777OP77", driver_name: "Пётр Иванов", scheduled_at: later(60), extra: true }, "Invalid request body: decode json"],
    ["неразбираемый scheduled_at раньше пустого driver_name", { vehicle_number: "B777OP77", driver_name: "", scheduled_at: "завтра" }, "Invalid request body: decode json"],
    ["destination_id не UUID раньше пустого driver_name", { destination_id: "x", vehicle_number: "B777OP77", driver_name: "", scheduled_at: later(60) }, "Invalid request body: decode json"],
  ])("создание: %s — INVALID_REQUEST", async (_, body, message) => {
    await loginAs("operator");
    await expect(createDispatch({ destination_id: seedIds.destinations["SHOP-7"], ...body })).rejects.toMatchObject({
      status: 400,
      code: "INVALID_REQUEST",
      message,
    });
  });

  it("создание: магазин не найден — 404 DESTINATION_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(
      createDispatch({ destination_id: unknownDestination, vehicle_number: "B777OP77", driver_name: "Пётр Иванов", scheduled_at: later(60) }),
    ).rejects.toMatchObject({ status: 404, code: "NOT_FOUND", message: "DESTINATION_NOT_FOUND" });
  });
});
