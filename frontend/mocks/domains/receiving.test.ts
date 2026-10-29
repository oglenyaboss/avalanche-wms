import { describe, expect, it } from "vitest";

import { api, unwrap } from "@/lib/api/client";
import { itemId } from "@/lib/signing/adr";
import { signAndSubmit, type SignedSubmit } from "@/lib/signing/sign-and-submit";

import { setChainMode } from "../chain";
import { db, newId, now, saveDb, type ProductRow } from "../db";
import { receivingIds } from "../data/fixtures/receiving";
import { idGroup, mockId, seedIds, seededCredentialIds } from "../data/fixture-kit";
import { nilUuid } from "../http";
import { loginAs, setupMockApi, waitForChain } from "../testing";
import { setAuthenticatorMode } from "../virtual-authenticator";

setupMockApi();

const { skus, bins } = seedIds;
const { shipments, cargoplaces, boxes: rcvBoxes, products: rcvProducts } = receivingIds;

const unknownShipment = mockId(idGroup.shipment, 999999);
const unknownCargoplace = mockId(idGroup.cargoplace, 999999);
const unknownBox = mockId(idGroup.box, 999999);
const unknownBin = mockId(idGroup.bin, 999999);
const unknownSku = mockId(idGroup.sku, 999999);
const longCode = "x".repeat(257);

const scanTtn = (ttn_code: string) => unwrap(api.POST("/receiving/gate/scan-ttn", { body: { ttn_code } }));
const scanGateCargoplace = (shipment_id: string, cargoplace_code: string) =>
  unwrap(api.POST("/receiving/gate/scan-cargoplace", { body: { shipment_id, cargoplace_code } }));
const acceptShipment = (shipment_id: string) => unwrap(api.POST("/receiving/gate/accept-shipment", { body: { shipment_id } }));
const scanTableCargoplace = (cargoplace_id: string) => unwrap(api.POST("/receiving/table/scan-cargoplace", { body: { cargoplace_id } }));
const scanBox = (cargoplace_id: string, box_barcode: string) => unwrap(api.POST("/receiving/table/scan-box", { body: { cargoplace_id, box_barcode } }));
const scanSku = (cargoplace_id: string, box_id: string, barcode: string) =>
  unwrap(api.POST("/receiving/table/scan-sku", { body: { cargoplace_id, box_id, barcode } }));
const scanQr = (cargoplace_id: string, box_id: string, sku_id: string, qr_code: string) =>
  unwrap(api.POST("/receiving/table/scan-qr", { body: { cargoplace_id, box_id, sku_id, qr_code } }));
const closeBox = (box_id: string) => unwrap(api.POST("/receiving/table/close-box", { body: { box_id } }));
const scanBuffer = (cargoplace_id: string, buffer_bin_id: string) =>
  unwrap(api.POST("/receiving/table/scan-buffer", { body: { cargoplace_id, buffer_bin_id } }));
const draft = (cargoplace_id: string) => unwrap(api.POST("/receiving/table/close-cargoplace/draft", { body: { cargoplace_id } }));
const submit = (body: SignedSubmit) => unwrap(api.POST("/receiving/table/close-cargoplace/submit", { body }));
const status = (operation_id: string) => unwrap(api.GET("/signed-actions/operations/{operation_id}", { params: { path: { operation_id } } }));

const cargoplaceStatus = (cargoplace_id: string) => db().cargoplaces.find((row) => row.cargoplace_id === cargoplace_id)?.status;

describe("ворота", () => {
  it("scan-ttn открывает CREATED-поставку и прогресс стабильный при повторном скане", async () => {
    await loginAs("operator");
    const result = await scanTtn("ТТН-2026-GATE-001");
    expect(result).toMatchObject({ status: "GATE_IN_PROGRESS", total_cargoplaces: 2, received_cargoplaces: 0 });
    const again = await scanTtn("ТТН-2026-GATE-001");
    expect(again.status).toBe("GATE_IN_PROGRESS");
    expect(db().receiving_gate.filter((row) => row.action === "SCAN_TTN")).toHaveLength(2);
    await expect(scanTtn("ТТН-2026-00142")).resolves.toMatchObject({ status: "RECEIVED" });
  });

  it("scan-ttn обрезает пробелы и считает только RECEIVED_AT_GATE", async () => {
    await loginAs("operator");
    const result = await scanTtn("  ТТН-2026-RCV-001  ");
    expect(result.ttn_code).toBe("ТТН-2026-RCV-001");
    expect(result.received_cargoplaces).toBe(1);
  });

  it("scan-ttn закрытой поставки — SHIPMENT_ALREADY_CLOSED", async () => {
    await loginAs("operator");
    await expect(scanTtn("ТТН-2026-RCV-002")).rejects.toMatchObject({ status: 409, code: "SHIPMENT_ALREADY_CLOSED", message: "Поставка уже закрыта" });
  });

  it.each([
    ["неизвестная ТТН", "ТТН-НЕТ", 404, "TTN_NOT_FOUND", "TTN не найдена"],
    ["пустой код", "   ", 400, "INVALID_REQUEST", "Невалидные входные данные"],
    ["длиннее 256", longCode, 400, "INVALID_REQUEST", "Невалидные входные данные"],
  ])("scan-ttn %s — %s", async (_, ttnCode, httpStatus, code, message) => {
    await loginAs("operator");
    await expect(scanTtn(ttnCode)).rejects.toMatchObject({ status: httpStatus, code, message });
  });

  it("лишнее поле в теле — INVALID_REQUEST «Невалидный JSON в теле запроса»", async () => {
    await loginAs("operator");
    const response = unwrap(api.POST("/receiving/gate/scan-ttn", { body: { ttn_code: "ТТН-2026-RCV-001", extra: 1 } as never }));
    await expect(response).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "Невалидный JSON в теле запроса" });
  });

  it("админ — FORBIDDEN, приёмка только для оператора", async () => {
    await loginAs("admin");
    await expect(scanTtn("ТТН-2026-RCV-001")).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Только оператор может выполнять это действие" });
  });

  it("gate/scan-cargoplace принимает EXPECTED, автозакрытие при полной приёме", async () => {
    await loginAs("operator");
    const first = await scanGateCargoplace(shipments["ТТН-2026-RCV-001"], "CP-RCV-002");
    expect(first.status).toBe("RECEIVED_AT_GATE");
    expect(first.progress).toEqual({ total: 3, received: 2, remaining: 1, not_received: 0 });
    const second = await scanGateCargoplace(shipments["ТТН-2026-RCV-001"], "CP-RCV-003");
    expect(second.progress).toEqual({ total: 3, received: 3, remaining: 0, not_received: 0 });
    expect(db().receiving_gate.filter((row) => row.action === "SHIPMENT_ACCEPTED")).toHaveLength(1);
    await expect(scanTtn("ТТН-2026-RCV-001")).rejects.toMatchObject({ code: "SHIPMENT_ALREADY_CLOSED" });
  });

  it.each([
    ["уже принято на воротах", "CP-RCV-001", 409, "CARGOPLACE_ALREADY_RECEIVED", "Грузоместо уже отсканировано"],
    ["код из другой поставки", "CP-GATE-001", 400, "CARGOPLACE_NOT_IN_SHIPMENT", "Грузоместо не принадлежит данной поставке"],
  ])("gate/scan-cargoplace: %s — %s", async (_, cargoplaceCode, httpStatus, code, message) => {
    await loginAs("operator");
    await expect(scanGateCargoplace(shipments["ТТН-2026-RCV-001"], cargoplaceCode)).rejects.toMatchObject({ status: httpStatus, code, message });
  });

  it.each([
    ["CREATED-поставка", seedIds.shipments["ТТН-2026-GATE-002"], 409, "SHIPMENT_NOT_IN_PROGRESS", "Поставка не в статусе GATE_IN_PROGRESS"],
    ["неизвестная поставка", unknownShipment, 404, "SHIPMENT_NOT_FOUND", "Поставка не найдена"],
    ["не UUID", "abc", 400, "INVALID_REQUEST", "shipment_id должен быть UUID"],
    ["nil-UUID", nilUuid, 400, "INVALID_REQUEST", "Невалидные входные данные"],
  ])("gate/scan-cargoplace: %s — %s", async (_, shipmentId, httpStatus, code, message) => {
    await loginAs("operator");
    await expect(scanGateCargoplace(shipmentId, cargoplaces["CP-RCV-002"])).rejects.toMatchObject({ status: httpStatus, code, message });
  });

  it("accept-shipment помечает EXPECTED как NOT_RECEIVED и закрывает поставку", async () => {
    await loginAs("operator");
    const result = await acceptShipment(shipments["ТТН-2026-RCV-001"]);
    expect(result).toMatchObject({
      shipment_id: shipments["ТТН-2026-RCV-001"],
      status: "GATE_CLOSED",
      summary: { total: 3, received: 1, remaining: 0, not_received: 2 },
    });
    expect(db().receiving_gate.filter((row) => row.action === "MARK_NOT_RECEIVED")).toHaveLength(1);
    expect(db().receiving_gate.filter((row) => row.action === "SHIPMENT_ACCEPTED")).toHaveLength(1);
    await expect(scanGateCargoplace(shipments["ТТН-2026-RCV-001"], "CP-RCV-002")).rejects.toMatchObject({ code: "SHIPMENT_NOT_IN_PROGRESS" });
    await expect(acceptShipment(shipments["ТТН-2026-RCV-001"])).rejects.toMatchObject({ code: "SHIPMENT_NOT_IN_PROGRESS" });
  });
});

describe("стол: грузоместа, коробки, товары", () => {
  it("table/scan-cargoplace открывает грузоместо, ожидания по имени SKU по возрастанию", async () => {
    await loginAs("operator");
    const result = await scanTableCargoplace(cargoplaces["CP-RCV-104"]);
    expect(result.status).toBe("TABLE_IN_PROGRESS");
    expect(result.expected_skus).toEqual([
      { sku_id: skus.nike, sku_name: "Кроссовки Nike Air Max 90", expected_qty: 2 },
      { sku_id: skus.puma, sku_name: "Рюкзак Puma Core", expected_qty: 1 },
    ]);
    expect(result.total_expected).toBe(3);
    await expect(scanTableCargoplace(cargoplaces["CP-RCV-104"])).rejects.toMatchObject({
      status: 409,
      code: "CARGOPLACE_NOT_RECEIVED_AT_GATE",
      message: "Грузоместо не в статусе RECEIVED_AT_GATE",
    });
  });

  it("table/scan-cargoplace неизвестного грузоместа — CARGOPLACE_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(scanTableCargoplace(unknownCargoplace)).rejects.toMatchObject({ status: 404, code: "CARGOPLACE_NOT_FOUND", message: "Грузоместо не найдено" });
  });

  it("scan-box открывает коробку, повторный скан возвращает ту же OPEN коробку", async () => {
    await loginAs("operator");
    const first = await scanBox(cargoplaces["CP-RCV-101"], "BOX-RCV-NEW");
    expect(first.status).toBe("OPEN");
    const second = await scanBox(cargoplaces["CP-RCV-101"], "BOX-RCV-NEW");
    expect(second.box_id).toBe(first.box_id);
    expect(db().boxes.filter((row) => row.cargoplace_id === cargoplaces["CP-RCV-101"] && row.box_barcode === "BOX-RCV-NEW")).toHaveLength(1);
  });

  it.each([
    ["закрытая коробка", cargoplaces["CP-RCV-101"], "BOX-RCV-102", 409, "BOX_NOT_OPEN", "Коробка не открыта"],
    ["грузоместо не в TABLE_IN_PROGRESS", cargoplaces["CP-RCV-103"], "BOX-RCV-NEW", 409, "CARGOPLACE_NOT_IN_PROGRESS", "Грузоместо не в статусе TABLE_IN_PROGRESS"],
  ])("scan-box: %s — %s", async (_, cargoplaceId, boxBarcode, httpStatus, code, message) => {
    await loginAs("operator");
    await expect(scanBox(cargoplaceId, boxBarcode)).rejects.toMatchObject({ status: httpStatus, code, message });
  });

  it("scan-box: тот же штрихкод в другом грузоместе — новая OPEN коробка", async () => {
    await loginAs("operator");
    const result = await scanBox(cargoplaces["CP-RCV-102"], "BOX-RCV-101");
    expect(result.status).toBe("OPEN");
    expect(result.box_id).not.toBe(rcvBoxes["BOX-RCV-101"]);
  });

  it("scan-sku находит SKU по штрихкоду", async () => {
    await loginAs("operator");
    const result = await scanSku(cargoplaces["CP-RCV-101"], rcvBoxes["BOX-RCV-101"], "4600000000011");
    expect(result).toEqual({ sku_id: skus.nike, sku_name: "Кроссовки Nike Air Max 90", barcode: "4600000000011", message: "Наклейте QR на товар" });
  });

  it.each([
    ["неизвестный штрихкод", rcvBoxes["BOX-RCV-101"], "0000", 404, "BARCODE_NOT_FOUND", "Штрихкод не найден"],
    ["коробка закрыта", rcvBoxes["BOX-RCV-102"], "4600000000011", 409, "BOX_NOT_OPEN", "Коробка не открыта"],
    ["коробка другого грузоместа", seedIds.boxes["BOX-001"], "4600000000011", 400, "BOX_NOT_IN_CARGOPLACE", "Коробка не принадлежит грузоместу"],
    ["неизвестная коробка", unknownBox, "4600000000011", 404, "BOX_NOT_FOUND", "Коробка не найдена"],
  ])("scan-sku: %s — %s", async (_, boxId, barcode, httpStatus, code, message) => {
    await loginAs("operator");
    await expect(scanSku(cargoplaces["CP-RCV-101"], boxId, barcode)).rejects.toMatchObject({ status: httpStatus, code, message });
  });

  it("scan-qr создаёт товар и считает прогресс", async () => {
    await loginAs("operator");
    const result = await scanQr(cargoplaces["CP-RCV-101"], rcvBoxes["BOX-RCV-101"], skus.nike, "QR-RCV-NEW-1");
    expect(result.status).toBe("RECEIVED");
    expect(result.progress).toEqual({ received_in_cargoplace: 4, expected_in_cargoplace: 5 });
    expect(db().products.find((row) => row.qr_code === "QR-RCV-NEW-1")).toMatchObject({
      sku_id: skus.nike,
      shipment_id: shipments["ТТН-2026-RCV-002"],
      cargoplace_id: cargoplaces["CP-RCV-101"],
      box_id: rcvBoxes["BOX-RCV-101"],
      bin_id: null,
      order_id: null,
      status: "RECEIVED",
    });
  });

  it("scan-qr занятого QR — QR_ALREADY_EXISTS, товар не создан", async () => {
    await loginAs("operator");
    const before = db().products.length;
    await expect(scanQr(cargoplaces["CP-RCV-101"], rcvBoxes["BOX-RCV-101"], skus.nike, "QR-2026-0001")).rejects.toMatchObject({
      status: 409,
      code: "QR_ALREADY_EXISTS",
      message: "QR-код уже зарегистрирован",
    });
    expect(db().products.length).toBe(before);
  });

  it("scan-qr проверяет SKU до грузоместа — SKU_NOT_FOUND даже у закрытого", async () => {
    await loginAs("operator");
    await expect(scanQr(cargoplaces["CP-RCV-103"], rcvBoxes["BOX-RCV-104"], unknownSku, "QR-RCV-NEW-2")).rejects.toMatchObject({
      status: 404,
      code: "SKU_NOT_FOUND",
      message: "SKU не найден",
    });
  });

  it("scan-qr: box_id и sku_id невалидны одновременно — ошибка называет box_id", async () => {
    await loginAs("operator");
    await expect(scanQr(cargoplaces["CP-RCV-101"], "x", "y", "QR")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "box_id должен быть UUID" });
  });

  it("scan-qr обрезает qr_code при поиске и записи", async () => {
    await loginAs("operator");
    const result = await scanQr(cargoplaces["CP-RCV-101"], rcvBoxes["BOX-RCV-101"], skus.nike, "  QR-RCV-TRIM-1  ");
    expect(result.qr_code).toBe("QR-RCV-TRIM-1");
    expect(db().products.some((row) => row.qr_code === "QR-RCV-TRIM-1")).toBe(true);
  });

  it("close-box закрывает коробку и считает товары, повтор — BOX_NOT_OPEN", async () => {
    await loginAs("operator");
    const result = await closeBox(rcvBoxes["BOX-RCV-101"]);
    expect(result).toEqual({ box_id: rcvBoxes["BOX-RCV-101"], status: "CLOSED", products_in_box: 1 });
    await expect(closeBox(rcvBoxes["BOX-RCV-101"])).rejects.toMatchObject({ status: 409, code: "BOX_NOT_OPEN" });
  });

  it("close-box в закрытом грузоместе — CARGOPLACE_NOT_IN_PROGRESS", async () => {
    await loginAs("operator");
    await expect(closeBox(rcvBoxes["BOX-RCV-104"])).rejects.toMatchObject({ status: 409, code: "CARGOPLACE_NOT_IN_PROGRESS" });
  });

  it("scan-buffer переносит товары закрытых коробок, товар из OPEN остаётся в null", async () => {
    await loginAs("operator");
    const result = await scanBuffer(cargoplaces["CP-RCV-101"], bins["BUFFER-01"]);
    expect(result).toEqual({ buffer_bin_id: bins["BUFFER-01"], buffer_code: "BUFFER-01", products_placed: 2 });
    expect(db().products.find((row) => row.product_id === rcvProducts["QR-RCV-0001"])?.bin_id).toBeNull();
    expect(db().products.find((row) => row.product_id === rcvProducts["QR-RCV-0002"])?.bin_id).toBe(bins["BUFFER-01"]);
  });

  it.each([
    ["ячейка хранения", bins["A-01-01"], 400, "BIN_NOT_BUFFER", "Ячейка не является буфером приёмки"],
    ["буфер отгрузки", bins["BIN-SHIP-5"], 400, "BIN_NOT_BUFFER", "Ячейка не является буфером приёмки"],
    ["неизвестная ячейка", unknownBin, 404, "BIN_NOT_FOUND", "Ячейка не найдена"],
  ])("scan-buffer: %s — %s", async (_, binId, httpStatus, code, message) => {
    await loginAs("operator");
    await expect(scanBuffer(cargoplaces["CP-RCV-101"], binId)).rejects.toMatchObject({ status: httpStatus, code, message });
  });

  it("scan-buffer без переносимых товаров — products_placed 0", async () => {
    await loginAs("operator");
    const result = await scanBuffer(cargoplaces["CP-RCV-102"], bins["BUFFER-01"]);
    expect(result.products_placed).toBe(0);
  });
});

describe("закрытие грузоместа", () => {
  it("draft возвращает операцию Receive по закрываемым товарам, ключ оператора", async () => {
    await loginAs("operator");
    const result = await draft(cargoplaces["CP-RCV-101"]);
    expect(result.action_type).toBe(1);
    expect(result.allow_credentials).toEqual([{ type: "public-key", id: seededCredentialIds.operator }]);
    expect(result.parts).toHaveLength(1);
    const expected = [rcvProducts["QR-RCV-0002"], rcvProducts["QR-RCV-0003"]].sort((a, b) => (itemId(a) < itemId(b) ? -1 : 1));
    expect(result.parts[0].item_ids).toEqual(expected);
  });

  it.each([
    ["пустое грузоместо", cargoplaces["CP-RCV-102"], 409, "CARGOPLACE_EMPTY", "В грузоместе нет принятых товаров для подтверждения"],
    ["грузоместо уже закрыто", cargoplaces["CP-RCV-103"], 409, "CARGOPLACE_NOT_IN_PROGRESS", "Грузоместо не в статусе TABLE_IN_PROGRESS"],
    ["неизвестное грузоместо", unknownCargoplace, 404, "CARGOPLACE_NOT_FOUND", "Грузоместо не найдено"],
  ])("draft %s — %s", async (_, cargoplaceId, httpStatus, code, message) => {
    await loginAs("operator");
    await expect(draft(cargoplaceId)).rejects.toMatchObject({ status: httpStatus, code, message });
  });

  it("у оператора без ключа — SIGNER_KEY_NOT_ACTIVE", async () => {
    await loginAs("operator2");
    await expect(draft(cargoplaces["CP-RCV-101"])).rejects.toMatchObject({ status: 409, code: "SIGNER_KEY_NOT_ACTIVE", message: "Ключ оператора не активен в реестре" });
  });

  it("submit закрывает грузоместо, сводка недостачи, статус SENT → APPLIED", async () => {
    await loginAs("operator");
    const result = await signAndSubmit(await draft(cargoplaces["CP-RCV-101"]), submit);
    expect(result).toEqual({
      cargoplace_id: cargoplaces["CP-RCV-101"],
      operation_id: expect.any(String),
      status: "TABLE_CLOSED",
      outbox_events_created: 1,
      summary: {
        products_received: 3,
        products_expected: 5,
        shortage: 2,
        shortage_by_sku: [
          { sku_name: "Кроссовки Nike Air Max 90", expected: 3, received: 2, shortage: 1 },
          { sku_name: "Футболка Adidas Originals", expected: 2, received: 1, shortage: 1 },
        ],
      },
    });
    expect(cargoplaceStatus(cargoplaces["CP-RCV-101"])).toBe("TABLE_CLOSED");
    expect(db().products.filter((row) => row.cargoplace_id === cargoplaces["CP-RCV-101"]).every((row) => row.status === "RECEIVED")).toBe(true);

    expect((await status(result.operation_id)).parts.map((part) => part.state)).toEqual(["SENT"]);
    await waitForChain();
    expect((await status(result.operation_id)).parts).toEqual([expect.objectContaining({ state: "APPLIED", reject_reason: null })]);
  });

  it("повторный submit — OPERATION_ALREADY_SUBMITTED", async () => {
    await loginAs("operator");
    const operation = await draft(cargoplaces["CP-RCV-101"]);
    let body: SignedSubmit | undefined;
    await signAndSubmit(operation, (signed) => {
      body = signed;
      return submit(signed);
    });
    await expect(submit(body!)).rejects.toMatchObject({ status: 409, code: "OPERATION_ALREADY_SUBMITTED" });
  });

  it("испорченная подпись — INVALID_ASSERTION, грузоместо остаётся TABLE_IN_PROGRESS", async () => {
    await loginAs("operator");
    setAuthenticatorMode("corrupt");
    await expect(signAndSubmit(await draft(cargoplaces["CP-RCV-101"]), submit)).rejects.toMatchObject({
      status: 400,
      code: "INVALID_ASSERTION",
      message: "Подтверждение не прошло проверку",
    });
    expect(cargoplaceStatus(cargoplaces["CP-RCV-101"])).toBe("TABLE_IN_PROGRESS");
  });

  it("новый товар в грузоместе после черновика — DRAFT_STALE", async () => {
    await loginAs("operator");
    const stale = await draft(cargoplaces["CP-RCV-101"]);
    const opened = await scanBox(cargoplaces["CP-RCV-101"], "BOX-RCV-NEW");
    await scanSku(cargoplaces["CP-RCV-101"], opened.box_id, "4600000000011");
    await scanQr(cargoplaces["CP-RCV-101"], opened.box_id, skus.nike, "QR-RCV-STALE-1");
    await closeBox(opened.box_id);
    await expect(signAndSubmit(stale, submit)).rejects.toMatchObject({
      status: 409,
      code: "DRAFT_STALE",
      message: "Состав грузоместа изменился, подтвердите закрытие заново",
    });
    expect(cargoplaceStatus(cargoplaces["CP-RCV-101"])).toBe("TABLE_IN_PROGRESS");
  });

  it("2001 товар — две части по ≤2000, submit проходит", async () => {
    await loginAs("operator");
    const state = db();
    const bulk = Array.from({ length: 2001 }, () => newId()).map((productId): ProductRow => ({
      product_id: productId,
      sku_id: skus.nike,
      shipment_id: shipments["ТТН-2026-RCV-002"],
      cargoplace_id: cargoplaces["CP-RCV-102"],
      box_id: rcvBoxes["BOX-RCV-103"],
      qr_code: `QR-RCV-BULK-${productId}`,
      bin_id: null,
      order_id: null,
      status: "RECEIVED",
      created_at: now(),
      updated_at: now(),
    }));
    state.products.push(...bulk);
    saveDb();

    const operation = await draft(cargoplaces["CP-RCV-102"]);
    expect(operation.parts).toHaveLength(2);
    expect(operation.parts[0].item_ids).toHaveLength(2000);
    expect(operation.parts[1].item_ids).toHaveLength(1);
    const ids = operation.parts.flatMap((part) => part.item_ids);
    const sorted = [...ids].sort((a, b) => (itemId(a) < itemId(b) ? -1 : itemId(a) > itemId(b) ? 1 : 0));
    expect(ids).toEqual(sorted);

    const result = await signAndSubmit(operation, submit);
    expect(result).toMatchObject({ status: "TABLE_CLOSED", outbox_events_created: 2 });
  });

  it("цепь отклонила часть — REJECTED с кодом причины", async () => {
    await loginAs("operator");
    setChainMode("reject:3");
    const result = await signAndSubmit(await draft(cargoplaces["CP-RCV-101"]), submit);
    await waitForChain();
    expect((await status(result.operation_id)).parts).toEqual([expect.objectContaining({ state: "REJECTED", reject_reason: 3 })]);
  });
});
