import type { Schema } from "@/lib/api/client";
import { actionType, detailsHash } from "@/lib/signing/adr";

import { chainEventRejected } from "../chain";
import { newId, now, type BinRow, type Db, type ProductRow } from "../db";
import { fail, guards, nilUuid, parseUuid, readJson, route } from "../http";
import { activeSigner, createOperation, draftView, lockSigner, occurredAtNow, partByItem, readSubmit, requireDraft, requireOperationSigner, verifyOperation, writeActions } from "../signed";

const invalidJson = "Невалидный JSON в теле запроса";
const maxProductIds = 200;

const invalidInput = () => fail(400, "INVALID_REQUEST", "Невалидные входные данные");
const bufferNotFound = () => fail(404, "BUFFER_BIN_NOT_FOUND", "Буферная ячейка не найдена");
const storageNotFound = () => fail(404, "STORAGE_BIN_NOT_FOUND", "Ячейка хранения не найдена");
const productNotFound = () => fail(404, "PRODUCT_NOT_FOUND", "Товар не найден");
const productNotInBuffer = () => fail(409, "PRODUCT_NOT_IN_BUFFER", "Товар не находится в указанной буферной ячейке");
const productNotReceived = () => fail(409, "PRODUCT_NOT_RECEIVED", "Товар не в статусе RECEIVED");
const cargoplaceNotClosed = () => fail(409, "CARGOPLACE_NOT_CLOSED", "Грузоместо товара ещё не закрыто на приёмке");
const draftStale = () => fail(409, "DRAFT_STALE", "Состав корзины изменился, подтвердите размещение заново");
const chainRejected = () => fail(409, "CHAIN_EVENT_REJECTED", "Размещение отклонено: приёмка товара не подтверждена в блокчейне (FAILED)");

const section = (bin: BinRow) => bin.section?.trim().toUpperCase() ?? null;

const bufferBin = (state: Db, binId: string) => state.bins.find((bin) => bin.bin_id === binId && section(bin) === "BUFFER");

const storageBin = (state: Db, binId: string) =>
  state.bins.find((bin) => bin.bin_id === binId && section(bin) !== null && section(bin) !== "BUFFER" && section(bin) !== "SHIPPING_BUFFER" && (bin.volume ?? 0) > 0);

const product = (state: Db, productId: string) => state.products.find((row) => row.product_id === productId);

const skuName = (state: Db, row: ProductRow) => state.skus.find((sku) => sku.sku_id === row.sku_id)?.name ?? "";

const cargoplaceClosed = (state: Db, cargoplaceId: string) => state.cargoplaces.find((cp) => cp.cargoplace_id === cargoplaceId)?.status === "TABLE_CLOSED";

function uuidField(value: unknown, field: string): string {
  const id = parseUuid(value);
  if (!id) throw fail(400, "INVALID_REQUEST", `${field} должен быть UUID`);
  return id;
}

export const putawayHandlers = [
  route("post", "/putaway/scan-buffer", guards.operator, async ({ db, request }) => {
    const body = await readJson(request, { buffer_bin_id: "string" }, invalidJson);
    const bufferBinId = uuidField(body.buffer_bin_id, "buffer_bin_id");
    if (bufferBinId === nilUuid) throw invalidInput();
    const bin = bufferBin(db, bufferBinId);
    if (!bin) throw bufferNotFound();
    const products = db.products
      .filter((row) => row.bin_id === bufferBinId && row.status === "RECEIVED")
      .sort((a, b) => a.created_at.localeCompare(b.created_at))
      .map((row) => ({ product_id: row.product_id, sku_name: skuName(db, row), qr_code: row.qr_code, status: row.status }));
    return { buffer_bin_id: bin.bin_id, buffer_code: bin.code, products, total_products: products.length } satisfies Schema<"PutawayScanBufferResponse">;
  }),

  route("post", "/putaway/scan-product", guards.operator, async ({ db, request }) => {
    const body = await readJson(request, { product_id: "string", buffer_bin_id: "string" }, invalidJson);
    const productId = uuidField(body.product_id, "product_id");
    const bufferBinId = uuidField(body.buffer_bin_id, "buffer_bin_id");
    if (productId === nilUuid || bufferBinId === nilUuid) throw invalidInput();
    if (!bufferBin(db, bufferBinId)) throw bufferNotFound();
    const row = product(db, productId);
    if (!row) throw productNotFound();
    if (row.bin_id !== bufferBinId) throw productNotInBuffer();
    if (row.status !== "RECEIVED") throw productNotReceived();
    const cartSize = db.products.filter((p) => p.bin_id === bufferBinId && p.status === "RECEIVED").length;
    return { product_id: row.product_id, sku_name: skuName(db, row), qr_code: row.qr_code, cart_size: cartSize } satisfies Schema<"PutawayScanProductResponse">;
  }),

  route("post", "/putaway/scan-storage-bin/draft", guards.operator, async ({ db, user, request }) => {
    const body = await readJson(request, { product_ids: "string[]", storage_bin_id: "string" }, invalidJson);
    const requested = body.product_ids ?? [];
    if (requested.length === 0) throw fail(400, "INVALID_REQUEST", "product_ids не может быть пустым");
    if (requested.length > maxProductIds) throw fail(400, "INVALID_REQUEST", `product_ids не может содержать более ${maxProductIds} элементов`);
    const productIds = requested.map((id) => uuidField(id, "product_id"));
    const storageBinId = uuidField(body.storage_bin_id, "storage_bin_id");
    if (storageBinId === nilUuid) throw invalidInput();
    if (!storageBin(db, storageBinId)) throw storageNotFound();

    const items = [...new Set(productIds)];
    for (const productId of items) {
      const row = product(db, productId);
      if (!row) throw productNotFound();
      if (!row.bin_id || !bufferBin(db, row.bin_id)) throw productNotInBuffer();
      if (row.status !== "RECEIVED") throw productNotReceived();
      if (!cargoplaceClosed(db, row.cargoplace_id)) throw cargoplaceNotClosed();
    }
    if (chainEventRejected(db, items, "receiving")) throw chainRejected();

    const signer = activeSigner(db, user.user_id);
    const occurredAt = occurredAtNow();
    const operation = createOperation(db, {
      actionType: actionType.PutAway,
      createdBy: user.user_id,
      signer,
      details: { storage_bin_id: storageBinId, occurred_at_ms: occurredAt.ms },
      items,
      detailsHash: (salt) => detailsHash(actionType.PutAway, { salt, storageBinId, operatorId: user.user_id, occurredAtMs: occurredAt.ms }),
    });
    return draftView(db, operation);
  }),

  route("post", "/putaway/scan-storage-bin/submit", guards.operator, async ({ db, user, request }) => {
    const { operationId, assertions } = await readSubmit(request, invalidJson);
    const operation = requireDraft(db, operationId, user.user_id, actionType.PutAway);
    const signer = requireOperationSigner(db, operation, user.user_id);
    await verifyOperation(operation, signer, assertions);

    const items = operation.parts.flatMap((part) => part.item_ids);
    if (chainEventRejected(db, items, "receiving")) throw chainRejected();

    const storageBinId = operation.details.storage_bin_id as string;
    const occurredAt = new Date(operation.details.occurred_at_ms as number).toISOString();
    const bin = storageBin(db, storageBinId);
    if (!bin) throw storageNotFound();

    const locked = [...items].sort().map((productId) => {
      const row = product(db, productId);
      if (!row) throw draftStale();
      return row;
    });
    for (const row of locked) {
      if (!row.bin_id || !bufferBin(db, row.bin_id)) throw draftStale();
      if (row.status !== "RECEIVED") throw draftStale();
      if (!cargoplaceClosed(db, row.cargoplace_id)) throw cargoplaceNotClosed();
    }
    lockSigner(db, operation.signer_credential_id);

    const actionByProduct = partByItem(operation);
    for (const row of locked) {
      db.putaways.push({
        event_id: newId(),
        product_id: row.product_id,
        from_bin_id: row.bin_id,
        bin_id: storageBinId,
        operator_id: user.user_id,
        action_id: actionByProduct.get(row.product_id) ?? null,
        occurred_at: occurredAt,
        created_at: now(),
      });
      row.bin_id = storageBinId;
      row.status = "STORED";
      row.updated_at = now();
    }
    writeActions(db, operation, assertions);

    return {
      storage_bin_id: bin.bin_id,
      storage_bin_code: bin.code,
      products_placed: items.length,
      outbox_events_created: operation.parts.length,
      operation_id: operation.operation_id,
    } satisfies Schema<"ScanStorageBinResponse">;
  }),
];
