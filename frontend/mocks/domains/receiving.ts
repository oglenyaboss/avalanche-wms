import type { Schema } from "@/lib/api/client";
import { actionType, detailsHash } from "@/lib/signing/adr";

import { newId, now, type BinRow, type Db, type InboundShipmentRow, type ReceivingGateRow, type ReceivingTableRow } from "../db";
import { fail, guards, nilUuid, parseUuid, readJson, route } from "../http";
import { activeSigner, createOperation, draftView, lockSigner, occurredAtNow, readSubmit, requireDraft, requireOperationSigner, sortByItemId, verifyOperation, writeActions } from "../signed";

const invalidJson = "Невалидный JSON в теле запроса";
const maxScanCode = 256;

const invalidInput = () => fail(400, "INVALID_REQUEST", "Невалидные входные данные");
const ttnNotFound = () => fail(404, "TTN_NOT_FOUND", "TTN не найдена");
const shipmentNotFound = () => fail(404, "SHIPMENT_NOT_FOUND", "Поставка не найдена");
const shipmentAlreadyClosed = () => fail(409, "SHIPMENT_ALREADY_CLOSED", "Поставка уже закрыта");
const shipmentNotInProgress = () => fail(409, "SHIPMENT_NOT_IN_PROGRESS", "Поставка не в статусе GATE_IN_PROGRESS");
const cargoplaceNotInShipment = () => fail(400, "CARGOPLACE_NOT_IN_SHIPMENT", "Грузоместо не принадлежит данной поставке");
const cargoplaceAlreadyReceived = () => fail(409, "CARGOPLACE_ALREADY_RECEIVED", "Грузоместо уже отсканировано");
const cargoplaceNotFound = () => fail(404, "CARGOPLACE_NOT_FOUND", "Грузоместо не найдено");
const cargoplaceNotReceivedAtGate = () => fail(409, "CARGOPLACE_NOT_RECEIVED_AT_GATE", "Грузоместо не в статусе RECEIVED_AT_GATE");
const cargoplaceNotInProgress = () => fail(409, "CARGOPLACE_NOT_IN_PROGRESS", "Грузоместо не в статусе TABLE_IN_PROGRESS");
const cargoplaceEmpty = () => fail(409, "CARGOPLACE_EMPTY", "В грузоместе нет принятых товаров для подтверждения");
const draftStale = () => fail(409, "DRAFT_STALE", "Состав грузоместа изменился, подтвердите закрытие заново");
const boxNotFound = () => fail(404, "BOX_NOT_FOUND", "Коробка не найдена");
const boxNotOpen = () => fail(409, "BOX_NOT_OPEN", "Коробка не открыта");
const boxNotInCargoplace = () => fail(400, "BOX_NOT_IN_CARGOPLACE", "Коробка не принадлежит грузоместу");
const barcodeNotFound = () => fail(404, "BARCODE_NOT_FOUND", "Штрихкод не найден");
const skuNotFound = () => fail(404, "SKU_NOT_FOUND", "SKU не найден");
const qrAlreadyExists = () => fail(409, "QR_ALREADY_EXISTS", "QR-код уже зарегистрирован");
const binNotFound = () => fail(404, "BIN_NOT_FOUND", "Ячейка не найдена");
const binNotBuffer = () => fail(400, "BIN_NOT_BUFFER", "Ячейка не является буфером приёмки");

function uuidField(value: unknown, field: string): string {
  const id = parseUuid(value);
  if (!id) throw fail(400, "INVALID_REQUEST", `${field} должен быть UUID`);
  return id;
}

function scanCode(value: string | undefined): string {
  const code = value?.trim() ?? "";
  if (code === "" || code.length > maxScanCode) throw invalidInput();
  return code;
}

const shipmentByTTN = (state: Db, ttnCode: string): InboundShipmentRow | undefined => state.inbound_shipments.find((row) => row.ttn_code === ttnCode);

const shipmentById = (state: Db, shipmentId: string): InboundShipmentRow | undefined => state.inbound_shipments.find((row) => row.shipment_id === shipmentId);

const cargoplace = (state: Db, cargoplaceId: string) => state.cargoplaces.find((row) => row.cargoplace_id === cargoplaceId);

const cargoplaceInShipment = (state: Db, shipmentId: string, cargoplaceCode: string) =>
  state.cargoplaces.find((row) => row.shipment_id === shipmentId && row.cargoplace_code === cargoplaceCode);

const box = (state: Db, boxId: string) => state.boxes.find((row) => row.box_id === boxId);

const skuById = (state: Db, skuId: string) => state.skus.find((row) => row.sku_id === skuId);

const skuByBarcode = (state: Db, barcode: string) => {
  const skuId = state.sku_barcodes.find((row) => row.barcode === barcode)?.sku_id;
  return skuId ? skuById(state, skuId) : undefined;
};

const openSkusByName = (state: Db, cargoplaceId: string) =>
  state.expected_cargoplace_skus
    .filter((row) => row.cargoplace_id === cargoplaceId)
    .map((row) => ({ sku_id: row.sku_id, sku_name: skuById(state, row.sku_id)?.name ?? "", expected_qty: row.expected_qty }))
    .sort((a, b) => (a.sku_name < b.sku_name ? -1 : a.sku_name > b.sku_name ? 1 : 0));

const closedBox = (state: Db, boxId: string) => state.boxes.find((row) => row.box_id === boxId && row.status === "CLOSED");

const closableProductIds = (state: Db, cargoplaceId: string) =>
  state.products
    .filter((row) => row.cargoplace_id === cargoplaceId && row.status === "RECEIVED" && row.box_id !== null && !!closedBox(state, row.box_id))
    .map((row) => row.product_id)
    .sort();

const isBufferBin = (bin: BinRow) => bin.section?.trim().toUpperCase() === "BUFFER";

const countCargoplaces = (state: Db, shipmentId: string) => {
  const rows = state.cargoplaces.filter((row) => row.shipment_id === shipmentId);
  const received = rows.filter((row) => row.status === "RECEIVED_AT_GATE").length;
  return { total: rows.length, received };
};

const codeOrder = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

function gateLog(state: Db, params: { ttn_code?: string; cargoplace_code?: string; shipment_id?: string; cargoplace_id?: string; operator_id: string; action: string }) {
  const at = now();
  state.receiving_gate.push({
    event_id: newId(),
    ttn_code: params.ttn_code ?? null,
    cargoplace_code: params.cargoplace_code ?? null,
    shipment_id: params.shipment_id ?? null,
    cargoplace_id: params.cargoplace_id ?? null,
    operator_id: params.operator_id,
    action: params.action,
    occurred_at: at,
    created_at: at,
  } satisfies ReceivingGateRow);
}

function tableLog(
  state: Db,
  params: {
    cargoplace_id: string;
    box_id?: string;
    operator_id: string;
    action: string;
    box_barcode?: string;
    sku_id?: string;
    qr_code?: string;
    product_id?: string;
    buffer_bin_id?: string;
    operation_id?: string;
  },
  occurredAt?: string,
) {
  const at = occurredAt ?? now();
  state.receiving_table.push({
    event_id: newId(),
    cargoplace_id: params.cargoplace_id,
    box_id: params.box_id ?? null,
    operator_id: params.operator_id,
    action: params.action,
    box_barcode: params.box_barcode ?? null,
    sku_id: params.sku_id ?? null,
    qr_code: params.qr_code ?? null,
    product_id: params.product_id ?? null,
    buffer_bin_id: params.buffer_bin_id ?? null,
    operation_id: params.operation_id ?? null,
    occurred_at: at,
    created_at: now(),
  } satisfies ReceivingTableRow);
}

export const receivingHandlers = [
  route("post", "/receiving/gate/scan-ttn", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { ttn_code: "string" }, invalidJson);
    const ttnCode = scanCode(body.ttn_code);
    const row = shipmentByTTN(db, ttnCode);
    if (!row) throw ttnNotFound();
    if (row.status === "GATE_CLOSED") throw shipmentAlreadyClosed();
    if (row.status === "CREATED") row.status = "GATE_IN_PROGRESS";
    gateLog(db, { ttn_code: row.ttn_code, shipment_id: row.shipment_id, operator_id: user.user_id, action: "SCAN_TTN" });

    const cargoplaces = db.cargoplaces
      .filter((cp) => cp.shipment_id === row.shipment_id)
      .sort((a, b) => codeOrder(a.cargoplace_code, b.cargoplace_code))
      .map((cp) => ({ cargoplace_id: cp.cargoplace_id, cargoplace_code: cp.cargoplace_code, status: cp.status as Schema<"CargoplaceStatus"> }));
    const received = cargoplaces.filter((cp) => cp.status === "RECEIVED_AT_GATE").length;

    return {
      shipment_id: row.shipment_id,
      ttn_code: row.ttn_code,
      status: row.status as Schema<"ShipmentStatus">,
      cargoplaces,
      total_cargoplaces: cargoplaces.length,
      received_cargoplaces: received,
    } satisfies Schema<"ScanTTNResult">;
  }),

  route("post", "/receiving/gate/scan-cargoplace", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { shipment_id: "string", cargoplace_code: "string" }, invalidJson);
    const shipmentId = uuidField(body.shipment_id, "shipment_id");
    if (shipmentId === nilUuid) throw invalidInput();
    const cargoplaceCode = scanCode(body.cargoplace_code);
    const shipmentRow = shipmentById(db, shipmentId);
    if (!shipmentRow) throw shipmentNotFound();
    if (shipmentRow.status !== "GATE_IN_PROGRESS") throw shipmentNotInProgress();
    const cp = cargoplaceInShipment(db, shipmentId, cargoplaceCode);
    if (!cp) throw cargoplaceNotInShipment();
    if (cp.status !== "EXPECTED") throw cargoplaceAlreadyReceived();

    const receivedAt = now();
    cp.status = "RECEIVED_AT_GATE";
    cp.received_at_gate_at = receivedAt;
    gateLog(db, { ttn_code: shipmentRow.ttn_code, cargoplace_code: cp.cargoplace_code, shipment_id: shipmentId, cargoplace_id: cp.cargoplace_id, operator_id: user.user_id, action: "SCAN_CARGOPLACE" });

    const { total, received } = countCargoplaces(db, shipmentId);
    if (total > 0 && received >= total) {
      shipmentRow.status = "GATE_CLOSED";
      gateLog(db, { ttn_code: shipmentRow.ttn_code, shipment_id: shipmentId, operator_id: user.user_id, action: "SHIPMENT_ACCEPTED" });
    }

    return {
      cargoplace_id: cp.cargoplace_id,
      cargoplace_code: cp.cargoplace_code,
      status: "RECEIVED_AT_GATE",
      received_at_gate_at: receivedAt,
      progress: { total, received, remaining: total - received, not_received: 0 },
    } satisfies Schema<"ScanGateCargoplaceResult">;
  }),

  route("post", "/receiving/gate/accept-shipment", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { shipment_id: "string" }, invalidJson);
    const shipmentId = uuidField(body.shipment_id, "shipment_id");
    if (shipmentId === nilUuid) throw invalidInput();
    const shipmentRow = shipmentById(db, shipmentId);
    if (!shipmentRow) throw shipmentNotFound();
    if (shipmentRow.status !== "GATE_IN_PROGRESS") throw shipmentNotInProgress();

    const notReceivedBefore = db.cargoplaces.some((cp) => cp.shipment_id === shipmentId && cp.status === "EXPECTED");
    for (const cp of db.cargoplaces) {
      if (cp.shipment_id === shipmentId && cp.status === "EXPECTED") cp.status = "NOT_RECEIVED";
    }
    if (notReceivedBefore) gateLog(db, { shipment_id: shipmentId, operator_id: user.user_id, action: "MARK_NOT_RECEIVED" });
    shipmentRow.status = "GATE_CLOSED";
    gateLog(db, { ttn_code: shipmentRow.ttn_code, shipment_id: shipmentId, operator_id: user.user_id, action: "SHIPMENT_ACCEPTED" });

    const { total, received } = countCargoplaces(db, shipmentId);
    const notReceived = db.cargoplaces.filter((cp) => cp.shipment_id === shipmentId && cp.status === "NOT_RECEIVED").length;
    return {
      shipment_id: shipmentId,
      status: "GATE_CLOSED",
      summary: { total, received, remaining: 0, not_received: notReceived },
    } satisfies Schema<"AcceptShipmentResult">;
  }),

  route("post", "/receiving/table/scan-cargoplace", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { cargoplace_id: "string" }, invalidJson);
    const cargoplaceId = uuidField(body.cargoplace_id, "cargoplace_id");
    if (cargoplaceId === nilUuid) throw invalidInput();
    const cp = cargoplace(db, cargoplaceId);
    if (!cp) throw cargoplaceNotFound();
    if (cp.status !== "RECEIVED_AT_GATE") throw cargoplaceNotReceivedAtGate();
    const expectedSkus = openSkusByName(db, cargoplaceId);
    cp.status = "TABLE_IN_PROGRESS";
    tableLog(db, { cargoplace_id: cargoplaceId, operator_id: user.user_id, action: "SCAN_CARGOPLACE" });

    return {
      cargoplace_id: cargoplaceId,
      cargoplace_code: cp.cargoplace_code,
      status: "TABLE_IN_PROGRESS",
      expected_skus: expectedSkus,
      total_expected: expectedSkus.reduce((sum, row) => sum + row.expected_qty, 0),
    } satisfies Schema<"ScanTableCargoplaceResult">;
  }),

  route("post", "/receiving/table/scan-box", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { cargoplace_id: "string", box_barcode: "string" }, invalidJson);
    const cargoplaceId = uuidField(body.cargoplace_id, "cargoplace_id");
    if (cargoplaceId === nilUuid) throw invalidInput();
    const boxBarcode = scanCode(body.box_barcode);
    const cp = cargoplace(db, cargoplaceId);
    if (!cp) throw cargoplaceNotFound();
    if (cp.status !== "TABLE_IN_PROGRESS") throw cargoplaceNotInProgress();
    const existing = db.boxes.find((row) => row.cargoplace_id === cargoplaceId && row.box_barcode === boxBarcode);
    let row = existing;
    if (row && row.status !== "OPEN") throw boxNotOpen();
    if (!row) {
      const at = now();
      row = { box_id: newId(), cargoplace_id: cargoplaceId, box_barcode: boxBarcode, status: "OPEN", created_at: at, updated_at: at };
      db.boxes.push(row);
    }
    tableLog(db, { cargoplace_id: cargoplaceId, box_id: row.box_id, box_barcode: boxBarcode, operator_id: user.user_id, action: "SCAN_BOX" });

    return { box_id: row.box_id, box_barcode: row.box_barcode, status: row.status as Schema<"BoxStatus"> } satisfies Schema<"ScanBoxResult">;
  }),

  route("post", "/receiving/table/scan-sku", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { cargoplace_id: "string", box_id: "string", barcode: "string" }, invalidJson);
    const cargoplaceId = uuidField(body.cargoplace_id, "cargoplace_id");
    const boxId = uuidField(body.box_id, "box_id");
    if (cargoplaceId === nilUuid || boxId === nilUuid) throw invalidInput();
    const barcode = scanCode(body.barcode);
    const cp = cargoplace(db, cargoplaceId);
    if (!cp) throw cargoplaceNotFound();
    if (cp.status !== "TABLE_IN_PROGRESS") throw cargoplaceNotInProgress();
    const boxRow = box(db, boxId);
    if (!boxRow) throw boxNotFound();
    if (boxRow.cargoplace_id !== cargoplaceId) throw boxNotInCargoplace();
    if (boxRow.status !== "OPEN") throw boxNotOpen();
    const skuRow = skuByBarcode(db, barcode);
    if (!skuRow) throw barcodeNotFound();
    tableLog(db, { cargoplace_id: cargoplaceId, box_id: boxId, sku_id: skuRow.sku_id, operator_id: user.user_id, action: "SCAN_SKU" });

    return { sku_id: skuRow.sku_id, sku_name: skuRow.name, barcode, message: "Наклейте QR на товар" } satisfies Schema<"ScanSKUResult">;
  }),

  route("post", "/receiving/table/scan-qr", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { cargoplace_id: "string", box_id: "string", sku_id: "string", qr_code: "string" }, invalidJson);
    const cargoplaceId = uuidField(body.cargoplace_id, "cargoplace_id");
    const boxId = uuidField(body.box_id, "box_id");
    const skuId = uuidField(body.sku_id, "sku_id");
    if (cargoplaceId === nilUuid || boxId === nilUuid || skuId === nilUuid) throw invalidInput();
    const qrCode = scanCode(body.qr_code);
    const skuRow = skuById(db, skuId);
    if (!skuRow) throw skuNotFound();
    const cp = cargoplace(db, cargoplaceId);
    if (!cp) throw cargoplaceNotFound();
    if (cp.status !== "TABLE_IN_PROGRESS") throw cargoplaceNotInProgress();
    const boxRow = box(db, boxId);
    if (!boxRow) throw boxNotFound();
    if (boxRow.cargoplace_id !== cargoplaceId) throw boxNotInCargoplace();
    if (boxRow.status !== "OPEN") throw boxNotOpen();
    if (db.products.some((row) => row.qr_code === qrCode)) throw qrAlreadyExists();

    const productId = newId();
    const at = now();
    db.products.push({
      product_id: productId,
      sku_id: skuId,
      shipment_id: cp.shipment_id,
      cargoplace_id: cargoplaceId,
      box_id: boxId,
      qr_code: qrCode,
      bin_id: null,
      order_id: null,
      status: "RECEIVED",
      created_at: at,
      updated_at: at,
    });
    tableLog(db, { cargoplace_id: cargoplaceId, box_id: boxId, sku_id: skuId, qr_code: qrCode, product_id: productId, operator_id: user.user_id, action: "SCAN_QR" });

    const receivedInCargoplace = db.products.filter((row) => row.cargoplace_id === cargoplaceId).length;
    const expectedInCargoplace = db.expected_cargoplace_skus.filter((row) => row.cargoplace_id === cargoplaceId).reduce((sum, row) => sum + row.expected_qty, 0);
    return {
      product_id: productId,
      sku_id: skuId,
      sku_name: skuRow.name,
      qr_code: qrCode,
      status: "RECEIVED",
      progress: { received_in_cargoplace: receivedInCargoplace, expected_in_cargoplace: expectedInCargoplace },
    } satisfies Schema<"ScanQRResult">;
  }),

  route("post", "/receiving/table/close-box", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { box_id: "string" }, invalidJson);
    const boxId = uuidField(body.box_id, "box_id");
    if (boxId === nilUuid) throw invalidInput();
    const boxRow = box(db, boxId);
    if (!boxRow) throw boxNotFound();
    if (boxRow.status !== "OPEN") throw boxNotOpen();
    const cp = cargoplace(db, boxRow.cargoplace_id);
    if (!cp) throw cargoplaceNotFound();
    if (cp.status !== "TABLE_IN_PROGRESS") throw cargoplaceNotInProgress();
    boxRow.status = "CLOSED";
    tableLog(db, { cargoplace_id: boxRow.cargoplace_id, box_id: boxId, operator_id: user.user_id, action: "CLOSE_BOX" });

    return { box_id: boxId, status: "CLOSED", products_in_box: db.products.filter((row) => row.box_id === boxId).length } satisfies Schema<"CloseBoxResult">;
  }),

  route("post", "/receiving/table/scan-buffer", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { cargoplace_id: "string", buffer_bin_id: "string" }, invalidJson);
    const cargoplaceId = uuidField(body.cargoplace_id, "cargoplace_id");
    const bufferBinId = uuidField(body.buffer_bin_id, "buffer_bin_id");
    if (cargoplaceId === nilUuid || bufferBinId === nilUuid) throw invalidInput();
    const binRow = db.bins.find((row) => row.bin_id === bufferBinId);
    if (!binRow) throw binNotFound();
    if (!isBufferBin(binRow)) throw binNotBuffer();
    const cp = cargoplace(db, cargoplaceId);
    if (!cp) throw cargoplaceNotFound();
    if (cp.status !== "TABLE_IN_PROGRESS") throw cargoplaceNotInProgress();

    const moved = db.products.filter((row) => row.cargoplace_id === cargoplaceId && row.status === "RECEIVED" && row.box_id !== null && !!closedBox(db, row.box_id));
    for (const row of moved) {
      row.bin_id = bufferBinId;
      row.updated_at = now();
    }
    if (moved.length > 0) tableLog(db, { cargoplace_id: cargoplaceId, buffer_bin_id: bufferBinId, operator_id: user.user_id, action: "SCAN_BUFFER" });

    return { buffer_bin_id: bufferBinId, buffer_code: binRow.code, products_placed: moved.length } satisfies Schema<"ScanBufferReceivingResult">;
  }),

  route("post", "/receiving/table/close-cargoplace/draft", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { cargoplace_id: "string" }, invalidJson);
    const cargoplaceId = uuidField(body.cargoplace_id, "cargoplace_id");
    if (cargoplaceId === nilUuid) throw invalidInput();
    const cp = cargoplace(db, cargoplaceId);
    if (!cp) throw cargoplaceNotFound();
    if (cp.status !== "TABLE_IN_PROGRESS") throw cargoplaceNotInProgress();
    const items = closableProductIds(db, cargoplaceId);
    if (items.length === 0) throw cargoplaceEmpty();
    const signer = activeSigner(db, user.user_id);
    const occurredAt = occurredAtNow();
    const operation = createOperation(db, {
      actionType: actionType.Receive,
      createdBy: user.user_id,
      signer,
      details: { cargoplace_id: cargoplaceId, occurred_at_ms: occurredAt.ms },
      items,
      detailsHash: (salt) => detailsHash(actionType.Receive, { salt, cargoplaceId, operatorId: user.user_id, occurredAtMs: occurredAt.ms }),
    });
    return draftView(db, operation);
  }),

  route("post", "/receiving/table/close-cargoplace/submit", guards.operator, async ({ db, user, request }) => {
    const { operationId, assertions } = await readSubmit(request, invalidJson);
    const operation = requireDraft(db, operationId, user.user_id, actionType.Receive);
    const signer = requireOperationSigner(db, operation, user.user_id);
    await verifyOperation(operation, signer, assertions);

    const cargoplaceId = operation.details.cargoplace_id as string;
    const cp = cargoplace(db, cargoplaceId);
    if (!cp) throw cargoplaceNotFound();
    if (cp.status !== "TABLE_IN_PROGRESS") throw cargoplaceNotInProgress();

    const productIds = sortByItemId(closableProductIds(db, cargoplaceId));
    const opItems = operation.parts.flatMap((part) => part.item_ids);
    if (productIds.length !== opItems.length || productIds.some((productId, i) => productId !== opItems[i])) throw draftStale();

    lockSigner(db, operation.signer_credential_id);

    const receivedBySku = new Map<string, number>();
    for (const row of db.products) {
      if (row.cargoplace_id !== cargoplaceId || row.status !== "RECEIVED") continue;
      receivedBySku.set(row.sku_id, (receivedBySku.get(row.sku_id) ?? 0) + 1);
    }
    let productsReceived = 0;
    for (const received of receivedBySku.values()) productsReceived += received;

    let productsExpected = 0;
    let shortage = 0;
    const shortageBySKU: Schema<"ShortageBySKU">[] = [];
    for (const expected of openSkusByName(db, cargoplaceId)) {
      productsExpected += expected.expected_qty;
      const receivedQty = receivedBySku.get(expected.sku_id) ?? 0;
      const shortageQty = expected.expected_qty - receivedQty;
      if (shortageQty <= 0) continue;
      shortage += shortageQty;
      shortageBySKU.push({ sku_name: expected.sku_name, expected: expected.expected_qty, received: receivedQty, shortage: shortageQty });
    }

    cp.status = "TABLE_CLOSED";
    cp.updated_at = now();
    tableLog(
      db,
      { cargoplace_id: cargoplaceId, operator_id: user.user_id, action: "CLOSE_CARGO", operation_id: operationId },
      new Date(operation.details.occurred_at_ms as number).toISOString(),
    );
    writeActions(db, operation, assertions);

    return {
      cargoplace_id: cargoplaceId,
      operation_id: operationId,
      status: "TABLE_CLOSED",
      summary: { products_received: productsReceived, products_expected: productsExpected, shortage, shortage_by_sku: shortageBySKU },
      outbox_events_created: operation.parts.length,
    } satisfies Schema<"CloseCargoplaceResult">;
  }),
];
