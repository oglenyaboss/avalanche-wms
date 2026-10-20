import type {
  BufferProduct,
  BufferScan,
  PickedProductResult,
  StoragePlacement,
} from '@/entities/putaway'
import {
  mapSignedOperationDraft,
  toAssertionsBody,
  type PartAssertion,
  type SignedOperationDraft,
  type SignedOperationDraftResponse,
} from '@/entities/signed-action'
import { apiClient } from '@/shared/api'

// ── Wire shapes (snake_case, as returned by the WMS API) ────────────────────

interface ApiEnvelope<T> {
  success: boolean
  data: T | null
  error: { code: string; message: string } | null
}

// Non-2xx responses reject via axios. A 200 with success:false / null data
// would otherwise crash the mappers on a null dereference — guard against it.
//
// Invariant: the WMS API always pairs an error envelope (success:false) with a
// non-2xx HTTP status, so axios throws an AxiosError that errors.ts can
// classify. This guard only covers a contract violation (200 + success:false);
// it throws a plain Error and the flow surfaces the generic fallback message.
function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }

  return envelope.data
}

interface ProductBufferItemResponse {
  product_id: string
  sku_name: string
  qr_code: string
  status: string
}

interface ScanBufferResponse {
  buffer_bin_id: string
  buffer_code: string
  products: ProductBufferItemResponse[]
  total_products: number
}

interface ScanProductResponse {
  product_id: string
  sku_name: string
  qr_code: string
  cart_size: number
}

interface ScanStorageBinResponse {
  storage_bin_id: string
  storage_bin_code: string
  products_placed: number
  outbox_events_created: number
}

// scan-storage-bin/submit result: today's placement result plus the id of the
// signed operation that performed it.
interface PlaceToStorageBinResponse extends ScanStorageBinResponse {
  operation_id: string
}

// ── Mappers ─────────────────────────────────────────────────────────────────

function mapBufferProduct(item: ProductBufferItemResponse): BufferProduct {
  return {
    productId: item.product_id,
    skuName: item.sku_name,
    qrCode: item.qr_code,
    // The endpoint only ever returns RECEIVED products (SQL filters on it).
    status: 'RECEIVED',
  }
}

// ── Requests ─────────────────────────────────────────────────────────────────

export async function scanBuffer(bufferBinId: string): Promise<BufferScan> {
  const { data } = await apiClient.post<ApiEnvelope<ScanBufferResponse>>(
    '/putaway/scan-buffer',
    { buffer_bin_id: bufferBinId },
  )

  const result = unwrap(data)

  return {
    bufferBinId: result.buffer_bin_id,
    bufferCode: result.buffer_code,
    products: result.products.map(mapBufferProduct),
    totalProducts: result.total_products,
  }
}

export interface ScanProductInput {
  productId: string
  bufferBinId: string
}

export async function scanProduct({
  productId,
  bufferBinId,
}: ScanProductInput): Promise<PickedProductResult> {
  const { data } = await apiClient.post<ApiEnvelope<ScanProductResponse>>(
    '/putaway/scan-product',
    { product_id: productId, buffer_bin_id: bufferBinId },
  )

  const result = unwrap(data)

  return {
    productId: result.product_id,
    skuName: result.sku_name,
    qrCode: result.qr_code,
    cartSize: result.cart_size,
  }
}

export interface DraftPlaceToStorageBinInput {
  productIds: string[]
  storageBinId: string
}

// Drafts the signed putaway operation: the whole unplaced cart into one
// storage bin. Errors: STORAGE_BIN_NOT_FOUND, PRODUCT_NOT_IN_BUFFER,
// PRODUCT_NOT_RECEIVED, CHAIN_EVENT_REJECTED, SIGNER_KEY_NOT_ACTIVE.
export async function draftPlaceToStorageBin({
  productIds,
  storageBinId,
}: DraftPlaceToStorageBinInput): Promise<SignedOperationDraft> {
  const { data } = await apiClient.post<
    ApiEnvelope<SignedOperationDraftResponse>
  >('/putaway/scan-storage-bin/draft', {
    product_ids: productIds,
    storage_bin_id: storageBinId,
  })

  return mapSignedOperationDraft(unwrap(data))
}

// Submits the per-part assertions produced by signOperation. Extra errors:
// DRAFT_STALE, OPERATION_ALREADY_SUBMITTED, INVALID_ASSERTION.
export async function submitPlaceToStorageBin(
  operationId: string,
  assertions: PartAssertion[],
): Promise<StoragePlacement> {
  const { data } = await apiClient.post<ApiEnvelope<PlaceToStorageBinResponse>>(
    '/putaway/scan-storage-bin/submit',
    {
      operation_id: operationId,
      assertions: toAssertionsBody(assertions),
    },
  )

  const result = unwrap(data)

  return {
    storageBinId: result.storage_bin_id,
    storageBinCode: result.storage_bin_code,
    productsPlaced: result.products_placed,
    outboxEventsCreated: result.outbox_events_created,
    operationId: result.operation_id,
  }
}
