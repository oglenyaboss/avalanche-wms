import type { Schema } from "@/lib/api/client";
import { actionType, detailsHash } from "@/lib/signing/adr";

import { unconfirmedProducts } from "../chain";
import { newId, now, type Db, type DestinationRow, type OutboundDispatchRow, type ProductRow } from "../db";
import { fail, guards, nilUuid, parseUuid, readJson, route } from "../http";
import { activeSigner, createOperation, draftView, lockSigner, occurredAtNow, partByItem, readSubmit, requireDraft, requireOperationSigner, verifyOperation, writeActions } from "../signed";

const invalidJson = "Невалидный JSON в теле запроса";
const maxProductIds = 200;

const invalidInput = () => fail(400, "INVALID_REQUEST", "Невалидные входные данные");
const binNotFound = () => fail(404, "BIN_NOT_FOUND", "Ячейка не найдена");
const binNotShippingBuffer = () => fail(400, "BIN_NOT_SHIPPING_BUFFER", "Ячейка не является буфером отгрузки");
const dispatchNotFound = () => fail(404, "DISPATCH_NOT_FOUND", "Рейс не найден");
const dispatchAlreadyDeparted = () => fail(409, "DISPATCH_ALREADY_DEPARTED", "Рейс уже отправлен");
const dispatchCancelled = () => fail(409, "DISPATCH_CANCELLED", "Рейс отменён");
const destinationMismatch = () => fail(409, "DESTINATION_MISMATCH", "Рейс назначен на другую зону");
const dispatchNotAtGate = () => fail(409, "DISPATCH_NOT_AT_GATE", "Рейс не находится у ворот");
const bufferEmpty = () => fail(409, "BUFFER_EMPTY", "Буфер отгрузки пуст");
const productNotInBuffer = () => fail(409, "PRODUCT_NOT_IN_BUFFER", "Товар не найден в буфере или не готов к отгрузке");
const draftStale = () => fail(409, "DRAFT_STALE", "Состав отгрузки изменился, подтвердите отгрузку заново");
const pickingNotConfirmed = (count: number) =>
  fail(409, "PICKING_NOT_CONFIRMED", `Сборка части товаров (${count} шт.) ещё не подтверждена в цепи. Повторите отгрузку чуть позже`);

const decodeJson = () => fail(400, "INVALID_REQUEST", "Невалидный JSON в теле запроса");

const skuName = (state: Db, row: ProductRow) => state.skus.find((sku) => sku.sku_id === row.sku_id)?.name ?? "";
const binById = (state: Db, binId: string) => state.bins.find((bin) => bin.bin_id === binId);
const destinationById = (state: Db, destinationId: string | null) =>
  destinationId === null ? null : state.destinations.find((destination) => destination.destination_id === destinationId) ?? null;
const dispatchById = (state: Db, dispatchId: string) => state.outbound_dispatches.find((dispatch) => dispatch.dispatch_id === dispatchId);

const readyToShip = (state: Db, binId: string) =>
  state.products.filter((product) => product.bin_id === binId && product.status === "READY_TO_SHIP").sort((a, b) => a.created_at.localeCompare(b.created_at));

const externalOrderNo = (state: Db, orderId: string | null) =>
  orderId === null ? null : state.orders.find((order) => order.order_id === orderId)?.external_order_no ?? null;

function uuidField(value: unknown, field: string): string {
  const id = parseUuid(value);
  if (!id) throw fail(400, "INVALID_REQUEST", `${field} должен быть UUID`);
  return id;
}

const shippingBufferWithDestination = (state: Db, binId: string) => {
  const bin = binById(state, binId);
  if (!bin) throw binNotFound();
  const destination = destinationById(state, bin.destination_id);
  if (bin.section !== "SHIPPING_BUFFER" || !destination) throw binNotShippingBuffer();
  return { bin, destination };
};

const scanDriverView = (dispatch: OutboundDispatchRow, destination: DestinationRow) => ({
  dispatch_id: dispatch.dispatch_id,
  dispatch_code: dispatch.dispatch_code,
  vehicle_number: dispatch.vehicle_number,
  driver_name: dispatch.driver_name,
  driver_phone: dispatch.driver_phone,
  destination: { id: destination.destination_id, code: destination.code, name: destination.name },
  status: dispatch.status,
  arrived_at: dispatch.arrived_at,
} satisfies Schema<"ScanDriverResponse">);

const dispatchDestinationOf = (state: Db, dispatch: OutboundDispatchRow) => destinationById(state, dispatch.destination_id);

const atGateDispatch = (state: Db, code: string) =>
  state.outbound_dispatches.find((row) => row.dispatch_code === code && row.status === "SCHEDULED" && dispatchDestinationOf(state, row));

const dispatchStatuses: readonly string[] = ["SCHEDULED", "AT_GATE", "DEPARTED", "CANCELLED"] satisfies Schema<"OutboundDispatchStatus">[];

const uuidQuery = (raw: string | null) => {
  if (!raw) return null;
  const id = parseUuid(raw);
  if (!id) throw fail(400, "INVALID_REQUEST", "destination_id не uuid");
  return id;
};

const intQuery = (raw: string | null) => {
  if (!raw) return null;
  if (!/^[+-]?\d+$/.test(raw)) throw fail(400, "INVALID_REQUEST", "warehouse_id не число");
  return Number(raw);
};

const utcTodayStartMs = () => Date.parse(new Date().toISOString().slice(0, 10));

const utcStamp = () => {
  const iso = new Date().toISOString();
  return `${iso.slice(0, 4)}-${iso.slice(5, 7)}${iso.slice(8, 10)}`;
};

const nextDispatchCode = (state: Db) => {
  const seq = state.outbound_dispatches.filter((row) => Date.parse(row.created_at) >= utcTodayStartMs()).length + 1;
  return `DSP-${utcStamp()}-${String(seq).padStart(3, "0")}`;
};

const decodeBody = async (request: Request) => {
  try {
    return await readJson(
      request,
      { destination_id: "string", vehicle_number: "string", driver_name: "string", driver_phone: "string", scheduled_at: "string" },
      "Невалидный JSON в теле запроса",
    );
  } catch {
    throw decodeJson();
  }
};

const decodedUuid = (raw: string | undefined) => {
  if (raw === undefined) return nilUuid;
  const id = parseUuid(raw);
  if (!id) throw decodeJson();
  return id;
};

export const shippingHandlers = [
  route("post", "/shipping/scan-buffer", guards.operator, async ({ db, request }) => {
    const body = await readJson(request, { buffer_bin_id: "string" }, invalidJson);
    const bufferBinId = uuidField(body.buffer_bin_id, "buffer_bin_id");
    if (bufferBinId === nilUuid) throw invalidInput();
    const { bin, destination } = shippingBufferWithDestination(db, bufferBinId);
    const products = readyToShip(db, bufferBinId).map((product) => ({
      product_id: product.product_id,
      qr_code: product.qr_code,
      sku_name: skuName(db, product),
      order_external_no: externalOrderNo(db, product.order_id),
    }));
    return {
      buffer_bin: { id: bin.bin_id, code: bin.code, destination: { id: destination.destination_id, code: destination.code, name: destination.name } },
      products,
      count: products.length,
    } satisfies Schema<"ScanBufferResponse">;
  }),

  route("post", "/shipping/scan-driver", guards.operator, async ({ db, request }) => {
    const body = await readJson(request, { dispatch_code: "string" }, invalidJson);
    if ((body.dispatch_code ?? "") === "") throw invalidInput();
    const code = body.dispatch_code ?? "";
    const arrived = atGateDispatch(db, code);
    if (arrived) {
      arrived.status = "AT_GATE";
      arrived.arrived_at = now();
      arrived.updated_at = now();
    }
    const dispatch = arrived ?? db.outbound_dispatches.find((row) => row.dispatch_code === code);
    const destination = dispatch ? dispatchDestinationOf(db, dispatch) : null;
    if (!dispatch || !destination) throw dispatchNotFound();
    if (arrived) return scanDriverView(dispatch, destination);
    if (dispatch.status === "DEPARTED") throw dispatchAlreadyDeparted();
    if (dispatch.status === "CANCELLED") throw dispatchCancelled();
    if (dispatch.status !== "AT_GATE") throw invalidInput();
    return scanDriverView(dispatch, destination);
  }),

  route("post", "/shipping/ship/draft", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { buffer_bin_id: "string", dispatch_id: "string", product_ids: "string[]" }, invalidJson);
    const bufferBinId = uuidField(body.buffer_bin_id, "buffer_bin_id");
    const dispatchId = uuidField(body.dispatch_id, "dispatch_id");
    const requested = body.product_ids ?? [];
    if (requested.length > maxProductIds) throw fail(400, "INVALID_REQUEST", `product_ids не может содержать более ${maxProductIds} элементов`);
    const productIds = requested.map((raw) => {
      const productId = parseUuid(raw);
      if (!productId) throw fail(400, "INVALID_REQUEST", "product_ids должен содержать только UUID");
      return productId;
    });
    if (bufferBinId === nilUuid || dispatchId === nilUuid) throw invalidInput();

    const { bin } = shippingBufferWithDestination(db, bufferBinId);
    const dispatch = dispatchById(db, dispatchId);
    if (!dispatch) throw dispatchNotFound();
    if (bin.destination_id !== dispatch.destination_id) throw destinationMismatch();
    if (dispatch.status !== "AT_GATE") throw dispatchNotAtGate();

    const products = productIds.length === 0 ? readyToShip(db, bufferBinId) : readyToShip(db, bufferBinId).filter((product) => productIds.includes(product.product_id));
    if (productIds.length === 0 && products.length === 0) throw bufferEmpty();
    if (productIds.length > 0 && products.length !== productIds.length) throw productNotInBuffer();

    const items = products.map((product) => product.product_id);
    const orderByProduct = new Map(products.map((product) => [product.product_id, product.order_id] as const));
    const orderIdsOf = (partItems: string[]) =>
      partItems.flatMap((productId) => {
        const orderId = orderByProduct.get(productId);
        return orderId === null || orderId === undefined ? [] : [orderId];
      });
    const unconfirmed = unconfirmedProducts(db, items, "picking");
    if (unconfirmed.length > 0) throw pickingNotConfirmed(unconfirmed.length);

    const signer = activeSigner(db, user.user_id);
    const occurredAt = occurredAtNow();
    const operation = createOperation(db, {
      actionType: actionType.Ship,
      createdBy: user.user_id,
      signer,
      details: { dispatch_id: dispatchId, shipping_buffer_bin_id: bufferBinId, occurred_at_ms: occurredAt.ms },
      items,
      detailsHash: (salt, partItems) =>
        detailsHash(actionType.Ship, {
          salt,
          dispatchId,
          shippingBufferBinId: bufferBinId,
          orderIds: orderIdsOf(partItems),
          operatorId: user.user_id,
          occurredAtMs: occurredAt.ms,
        }),
    });
    return draftView(db, operation);
  }),

  route("post", "/shipping/ship/submit", guards.operator, async ({ db, user, request }) => {
    const { operationId, assertions } = await readSubmit(request, invalidJson);
    const operation = requireDraft(db, operationId, user.user_id, actionType.Ship);
    const signer = requireOperationSigner(db, operation, user.user_id);
    await verifyOperation(operation, signer, assertions);

    const items = operation.parts.flatMap((part) => part.item_ids);
    const unconfirmed = unconfirmedProducts(db, items, "picking");
    if (unconfirmed.length > 0) throw pickingNotConfirmed(unconfirmed.length);

    const bufferBinId = operation.details.shipping_buffer_bin_id as string;
    const dispatchId = operation.details.dispatch_id as string;
    const occurredAt = new Date(operation.details.occurred_at_ms as number).toISOString();

    const { bin } = shippingBufferWithDestination(db, bufferBinId);
    const dispatch = dispatchById(db, dispatchId);
    if (!dispatch) throw dispatchNotFound();
    if (bin.destination_id !== dispatch.destination_id) throw destinationMismatch();
    if (dispatch.status !== "AT_GATE") throw dispatchNotAtGate();

    const products = readyToShip(db, bufferBinId).filter((product) => items.includes(product.product_id));
    if (products.length !== items.length) throw draftStale();

    lockSigner(db, operation.signer_credential_id);

    for (const product of products) product.status = "SHIPPED";

    const actionByProduct = partByItem(operation);
    for (const part of operation.parts) {
      for (const productId of part.item_ids) {
        db.shippings.push({
          event_id: newId(),
          product_id: productId,
          operator_id: user.user_id,
          dispatch_id: dispatchId,
          action_id: actionByProduct.get(productId) ?? null,
          shipped_at: occurredAt,
          occurred_at: occurredAt,
          created_at: now(),
        });
      }
    }

    let ordersCompleted = 0;
    let ordersPartiallyShipped = 0;
    for (const orderId of new Set(products.map((product) => product.order_id))) {
      const order = orderId === null ? null : db.orders.find((row) => row.order_id === orderId);
      const advancing = order?.status === "ALLOCATED" || order?.status === "ASSEMBLED" || order?.status === "PARTIALLY_SHIPPED";
      if (!order || !advancing) continue;
      const orderProducts = db.products.filter((product) => product.order_id === orderId);
      if (!orderProducts.some((product) => product.status === "SHIPPED")) continue;
      const allShipped = orderProducts.every((product) => product.status === "SHIPPED");
      if (allShipped) ordersCompleted += 1;
      else ordersPartiallyShipped += 1;
      order.status = allShipped ? "SHIPPED" : "PARTIALLY_SHIPPED";
      order.updated_at = now();
    }

    const bufferBinIds = new Set(
      db.bins
        .filter((row) => row.destination_id === bin.destination_id && (row.section ?? "").trim().toUpperCase() === "SHIPPING_BUFFER")
        .map((row) => row.bin_id),
    );
    const bufferRemaining = db.products.filter((product) => product.bin_id !== null && bufferBinIds.has(product.bin_id) && product.status === "READY_TO_SHIP").length;

    let dispatchDeparted = false;
    if (bufferRemaining === 0) {
      dispatch.status = "DEPARTED";
      dispatch.departed_at = now();
      dispatch.updated_at = now();
      dispatchDeparted = true;
    }

    writeActions(db, operation, assertions);

    return {
      products_shipped: items.length,
      outbox_events_created: operation.parts.length,
      orders_completed: ordersCompleted,
      orders_partially_shipped: ordersPartiallyShipped,
      dispatch_departed: dispatchDeparted,
      buffer_remaining: bufferRemaining,
      operation_id: operation.operation_id,
    } satisfies Schema<"ShipResponse">;
  }),

  route("get", "/dispatches/", guards.operator, ({ db, url }) => {
    let rows = [...db.outbound_dispatches];
    const status = url.searchParams.get("status");
    if (status) {
      if (!dispatchStatuses.includes(status)) throw fail(400, "INVALID_REQUEST", "неизвестный статус рейса");
      rows = rows.filter((row) => row.status === status);
    }
    const destinationId = uuidQuery(url.searchParams.get("destination_id"));
    if (destinationId !== null && destinationId !== nilUuid) rows = rows.filter((row) => row.destination_id === destinationId);
    const warehouseId = intQuery(url.searchParams.get("warehouse_id"));
    if (warehouseId !== null && warehouseId !== -1) rows = rows.filter((row) => row.warehouse_id === warehouseId);
    return rows.sort((a, b) => a.scheduled_at.localeCompare(b.scheduled_at)) satisfies Schema<"OutboundDispatch">[];
  }),

  route("get", "/dispatches/:dispatchId", guards.operator, ({ db, params }) => {
    const dispatchId = parseUuid(params.dispatchId);
    if (!dispatchId) throw fail(400, "INVALID_REQUEST", "dispatch_id не uuid");
    const dispatch = dispatchById(db, dispatchId);
    if (!dispatch) throw dispatchNotFound();
    return { dispatch } satisfies { dispatch: Schema<"OutboundDispatch"> };
  }),

  route("post", "/dispatches/", guards.operator, async ({ db, request }) => {
    const body = await decodeBody(request);
    const scheduledMs = body.scheduled_at === undefined ? 0 : Date.parse(body.scheduled_at);
    if (Number.isNaN(scheduledMs)) throw decodeJson();
    const destinationId = decodedUuid(body.destination_id);
    if ((body.driver_name ?? "") === "") throw fail(400, "INVALID_REQUEST", "driver_name обязателен");
    if ((body.vehicle_number ?? "") === "") throw fail(400, "INVALID_REQUEST", "vehicle_number обязателен");
    if (scheduledMs < Date.now() - 60_000) throw fail(400, "INVALID_REQUEST", "scheduled_at не может быть в прошлом");
    const destination = destinationById(db, destinationId);
    if (!destination) throw fail(404, "DESTINATION_NOT_FOUND", "Магазин не найден");
    const dispatch: OutboundDispatchRow = {
      dispatch_id: newId(),
      dispatch_code: nextDispatchCode(db),
      warehouse_id: destination.warehouse_id,
      destination_id: destinationId,
      vehicle_number: body.vehicle_number ?? "",
      driver_name: body.driver_name ?? "",
      driver_phone: body.driver_phone ?? null,
      status: "SCHEDULED",
      scheduled_at: new Date(scheduledMs).toISOString(),
      arrived_at: null,
      departed_at: null,
      created_at: now(),
      updated_at: now(),
    };
    db.outbound_dispatches.push(dispatch);
    return { dispatch } satisfies { dispatch: Schema<"OutboundDispatch"> };
  }),
];
