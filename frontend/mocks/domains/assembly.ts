import type { Schema } from "@/lib/api/client";
import { actionType, detailsHash } from "@/lib/signing/adr";

import { chainEventRejected } from "../chain";
import { newId, now, type Db, type ProductRow } from "../db";
import { fail, guards, nilUuid, parseUuid, readJson, route, type Ctx } from "../http";
import { activeSigner, createOperation, draftView, lockSigner, occurredAtNow, readSubmit, requireDraft, requireOperationSigner, verifyOperation, writeActions } from "../signed";

const invalidJson = "Невалидный JSON в теле запроса";
const allocateBatchLimit = 500;

const invalidInput = () => fail(400, "INVALID_REQUEST", "Невалидные входные данные");
const destinationNotFound = () => fail(404, "DESTINATION_NOT_FOUND", "магазин не найден");
const noTaskForProduct = () => fail(409, "NO_TASK_FOR_PRODUCT", "Нет задачи сборки для этого товара");
const productNotAllocated = () => fail(409, "PRODUCT_NOT_ALLOCATED", "Товар не в статусе ALLOCATED");
const internalError = () => fail(500, "INTERNAL_ERROR", "Внутренняя ошибка сервера");
const chainRejected = () => fail(409, "CHAIN_EVENT_REJECTED", "Подбор отклонён: размещение товара не подтверждено в блокчейне (FAILED)");
const binNotShippingBuffer = () => fail(400, "BIN_NOT_SHIPPING_BUFFER", "Ячейка не является буфером отгрузки");
const cartEmpty = () => fail(409, "CART_EMPTY", "Корзина оператора пуста");
const destinationMismatch = () => fail(409, "DESTINATION_MISMATCH", "Буфер отгрузки другого магазина: собранные товары не для этого пункта назначения");
const draftStale = () => fail(409, "DRAFT_STALE", "Состав корзины изменился, подтвердите выгрузку заново");

const taskStatuses = ["PENDING", "IN_PROGRESS", "DONE", "CANCELLED"];

const uuidField = (value: unknown, message: string): string => {
  const id = parseUuid(value);
  if (!id) throw fail(400, "INVALID_REQUEST", message);
  return id;
};

const binOf = (state: Db, binId: string) => state.bins.find((row) => row.bin_id === binId);
const productOf = (state: Db, productId: string) => state.products.find((row) => row.product_id === productId);
const skuByName = (state: Db, skuId: string) => state.skus.find((row) => row.sku_id === skuId)?.name ?? "";

const shippingBufferBin = (state: Db, binId: string) => binOf(state, binId)?.section === "SHIPPING_BUFFER" ? binOf(state, binId) : undefined;

const cartCount = (state: Db, operatorId: string): number =>
  new Set(
    state.assembly_tasks
      .filter((task) => task.operator_id === operatorId && task.status === "DONE" && task.action_id === null && productOf(state, task.product_id)?.status === "ASSEMBLED")
      .map((task) => task.product_id),
  ).size;

const operatorBasket = (state: Db, operatorId: string, destinationId: string): ProductRow[] => {
  const rows = state.assembly_tasks
    .filter((task) => task.operator_id === operatorId && task.status === "DONE" && task.action_id === null && task.destination_id === destinationId)
    .map((task) => productOf(state, task.product_id))
    .filter((row): row is ProductRow => row !== undefined && row.status === "ASSEMBLED");
  return [...new Map(rows.map((row) => [row.product_id, row])).values()].sort((a, b) => (a.product_id < b.product_id ? -1 : 1));
};

const allocate = async ({ db: state, request }: Ctx) => {
  const body = await readJson(request, { destination_id: "string" }, invalidJson);
  const destinationId = uuidField(body.destination_id, "destination_id не uuid");
  if (destinationId === nilUuid) throw invalidInput();

  const orders = state.orders
    .filter((order) => order.destination_id === destinationId && order.status === "NEW")
    .sort((a, b) => a.created_at.localeCompare(b.created_at))
    .slice(0, allocateBatchLimit);

  let allocatedOrders = 0;
  let allocatedProducts = 0;
  const insufficientOrders: Schema<"InsufficientOrder">[] = [];

  for (const order of orders) {
    const lines = state.order_lines.filter((line) => line.order_id === order.order_id);
    const missing: Schema<"InsufficientSKU">[] = [];
    const reserved: ProductRow[] = [];
    for (const line of lines) {
      const available = state.products
        .filter((row) => row.sku_id === line.sku_id && row.status === "STORED" && row.order_id === null)
        .sort((a, b) => a.created_at.localeCompare(b.created_at) || (a.product_id < b.product_id ? -1 : 1))
        .slice(0, line.qty);
      if (available.length < line.qty) {
        missing.push({ sku_id: line.sku_id, sku_name: skuByName(state, line.sku_id), missing_qty: line.qty - available.length });
        continue;
      }
      reserved.push(...available);
    }
    if (missing.length > 0) {
      insufficientOrders.push({ order_id: order.order_id, missing_skus: missing });
      continue;
    }
    const occurredAt = now();
    for (const row of reserved) {
      const bin = row.bin_id ? binOf(state, row.bin_id) : undefined;
      if (!bin) throw internalError();
      row.status = "ALLOCATED";
      row.order_id = order.order_id;
      row.updated_at = occurredAt;
      state.assembly_tasks.push({
        event_id: newId(),
        order_id: order.order_id,
        product_id: row.product_id,
        sku_id: row.sku_id,
        from_bin_id: row.bin_id!,
        section: bin.section ?? "",
        destination_id: destinationId,
        status: "PENDING",
        operator_id: null,
        action_id: null,
        occurred_at: occurredAt,
        unloaded_at: null,
        created_at: occurredAt,
        updated_at: occurredAt,
      });
    }
    order.status = "ALLOCATED";
    order.updated_at = occurredAt;
    allocatedOrders += 1;
    allocatedProducts += reserved.length;
  }

  return { allocated_orders: allocatedOrders, allocated_products: allocatedProducts, insufficient_orders: insufficientOrders } satisfies Schema<"AllocateResponse">;
};

const getTasks = ({ db: state, url }: Ctx) => {
  const destinationId = uuidField(url.searchParams.get("destination_id") ?? "", url.searchParams.get("destination_id") ? "destination_id не uuid" : "destination_id обязателен");
  const rawOperator = url.searchParams.get("operator_id");
  const operatorId = rawOperator ? uuidField(rawOperator, "operator_id не uuid") : nilUuid;
  const rawStatus = url.searchParams.get("status") ?? "";
  const status = rawStatus === "" ? "PENDING" : rawStatus;
  if (!taskStatuses.includes(status)) throw fail(400, "INVALID_REQUEST", "неизвестный статус задачи");
  if (destinationId === nilUuid) throw invalidInput();

  const tasks = state.assembly_tasks
    .filter((task) => task.destination_id === destinationId && task.status === status && (operatorId === nilUuid || task.operator_id === null || task.operator_id === operatorId))
    .flatMap((task) => {
      const product = productOf(state, task.product_id);
      const bin = binOf(state, task.from_bin_id);
      const sku = state.skus.find((row) => row.sku_id === task.sku_id);
      const order = state.orders.find((row) => row.order_id === task.order_id);
      if (!product || !bin || !sku || !order) return [];
      return [
        {
          task_id: task.event_id,
          product_id: task.product_id,
          qr_code: product.qr_code,
          sku_name: sku.name,
          from_bin_code: bin.code,
          from_bin_section: bin.section ?? "",
          order_no: order.external_order_no,
        },
      ];
    });

  tasks.sort((a, b) => {
    if (a.from_bin_section !== b.from_bin_section) {
      if (!a.from_bin_section) return 1;
      if (!b.from_bin_section) return -1;
      return a.from_bin_section < b.from_bin_section ? -1 : 1;
    }
    if (a.from_bin_code !== b.from_bin_code) return a.from_bin_code < b.from_bin_code ? -1 : 1;
    return a.product_id < b.product_id ? -1 : a.product_id > b.product_id ? 1 : 0;
  });

  return { tasks } satisfies Schema<"TaskResponse">;
};

const pick = async ({ db: state, user, request }: Ctx) => {
  const body = await readJson(request, { product_id: "string" }, invalidJson);
  const productId = uuidField(body.product_id, "product_id не uuid");
  if (productId === nilUuid) throw invalidInput();

  if (chainEventRejected(state, [productId], "putaway")) throw chainRejected();

  const task = state.assembly_tasks.find((row) => row.product_id === productId && row.status === "PENDING");
  if (!task) throw noTaskForProduct();
  const product = productOf(state, productId);
  if (!product || product.status !== "ALLOCATED") throw productNotAllocated();

  const at = now();
  task.status = "DONE";
  task.operator_id = user.user_id;
  task.occurred_at = at;
  task.updated_at = at;
  product.status = "ASSEMBLED";
  product.updated_at = at;

  const cartSize = cartCount(state, user.user_id);
  return { product_id: productId, cart_size: cartSize } satisfies Schema<"PickResponse">;
};

const draftShipping = async ({ db: state, user, request }: Ctx) => {
  const body = await readJson(request, { buffer_bin_id: "string" }, invalidJson);
  const bufferBinId = uuidField(body.buffer_bin_id, "buffer_bin_id должен быть UUID");
  if (bufferBinId === nilUuid) throw invalidInput();

  const bin = shippingBufferBin(state, bufferBinId);
  if (!bin) throw binNotShippingBuffer();
  if (bin.destination_id === null) throw destinationNotFound();

  const basket = operatorBasket(state, user.user_id, bin.destination_id);
  if (basket.length === 0) {
    if (cartCount(state, user.user_id) > 0) throw destinationMismatch();
    throw cartEmpty();
  }
  const signer = activeSigner(state, user.user_id);

  const orderOf = new Map(basket.flatMap((row) => (row.order_id ? [[row.product_id, row.order_id] as const] : [])));
  const occurredAt = occurredAtNow();
  const operation = createOperation(state, {
    actionType: actionType.Pick,
    createdBy: user.user_id,
    signer,
    details: { shipping_buffer_bin_id: bin.bin_id, occurred_at_ms: occurredAt.ms },
    items: basket.map((row) => row.product_id),
    detailsHash: (salt, partItems) =>
      detailsHash(actionType.Pick, {
        salt,
        shippingBufferBinId: bin.bin_id,
        orderIds: partItems.map((id) => orderOf.get(id)).filter((id): id is string => id !== undefined),
        operatorId: user.user_id,
        occurredAtMs: occurredAt.ms,
      }),
  });
  return draftView(state, operation);
};

const submitShipping = async ({ db: state, user, request }: Ctx) => {
  const { operationId, assertions } = await readSubmit(request, invalidJson);
  const operation = requireDraft(state, operationId, user.user_id, actionType.Pick);
  const signer = requireOperationSigner(state, operation, user.user_id);
  await verifyOperation(operation, signer, assertions);

  const bin = shippingBufferBin(state, operation.details.shipping_buffer_bin_id as string);
  if (!bin) throw binNotShippingBuffer();
  if (bin.destination_id === null) throw destinationNotFound();

  const items = operation.parts.flatMap((part) => part.item_ids);
  const signedIds = new Set(items);
  const basket = operatorBasket(state, user.user_id, bin.destination_id);
  if (basket.length !== signedIds.size) throw draftStale();
  for (const row of basket) {
    if (!signedIds.has(row.product_id)) throw draftStale();
  }

  lockSigner(state, operation.signer_credential_id);

  const occurredAt = new Date(operation.details.occurred_at_ms as number).toISOString();
  const at = now();
  for (const row of basket) {
    row.bin_id = bin.bin_id;
    row.status = "READY_TO_SHIP";
    row.updated_at = at;
  }
  for (const part of operation.parts) {
    for (const task of state.assembly_tasks) {
      if (!part.item_ids.includes(task.product_id) || task.operator_id !== user.user_id || task.destination_id !== bin.destination_id || task.status !== "DONE" || task.action_id !== null) continue;
      task.action_id = part.action_id;
      task.unloaded_at = occurredAt;
      task.updated_at = at;
    }
  }

  const orderIds = new Set(basket.flatMap((row) => (row.order_id ? [row.order_id] : [])));
  let ordersAssembled = 0;
  for (const orderId of orderIds) {
    const order = state.orders.find((row) => row.order_id === orderId);
    if (!order || order.status !== "ALLOCATED") continue;
    if (state.products.some((row) => row.order_id === orderId && row.status !== "READY_TO_SHIP")) continue;
    order.status = "ASSEMBLED";
    order.updated_at = at;
    ordersAssembled += 1;
  }

  writeActions(state, operation, assertions);

  return { buffer_bin_id: bin.bin_id, products_placed: items.length, orders_assembled: ordersAssembled, operation_id: operation.operation_id } satisfies Schema<"ScanShippingBufferResponse">;
};

const getDestinations = ({ db: state, url }: Ctx) => {
  const raw = url.searchParams.get("warehouse_id") ?? "";
  let warehouseId: number | null = null;
  if (raw !== "") {
    const parsed = /^-?\d+$/.test(raw) ? Number(raw) : Number.NaN;
    if (Number.isNaN(parsed)) throw fail(400, "INVALID_REQUEST", "warehouse_id должен быть числом");
    if (parsed <= 0) throw fail(400, "INVALID_REQUEST", "warehouse_id должен быть положительным числом");
    warehouseId = parsed;
  }
  const destinations = state.destinations
    .filter((row) => warehouseId === null || row.warehouse_id === warehouseId)
    .sort((a, b) => (a.code < b.code ? -1 : a.code > b.code ? 1 : 0))
    .slice(0, 1000)
    .map((row) => ({ destination_id: row.destination_id, code: row.code, name: row.name, address: row.address, warehouse_id: row.warehouse_id }));
  return { destinations } satisfies Schema<"DestinationListResponse">;
};

export const assemblyHandlers = [
  route("post", "/assembly/allocate", guards.adminOrOperator, allocate),
  route("get", "/assembly/tasks", guards.operator, getTasks),
  route("post", "/assembly/pick", guards.operator, pick),
  route("post", "/assembly/scan-shipping-buffer/draft", guards.operator, draftShipping),
  route("post", "/assembly/scan-shipping-buffer/submit", guards.operator, submitShipping),
  route("get", "/destinations", guards.operator, getDestinations),
  route("get", "/destinations/", guards.operator, getDestinations),
];
