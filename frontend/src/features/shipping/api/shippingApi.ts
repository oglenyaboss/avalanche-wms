import type {
  DispatchInfo,
  OutboundDispatchStatus,
  ScanBufferResult,
  ShipResult,
  ShippingBufferProduct,
  ShippingDestination,
} from '@/entities/shipping'
import { apiClient } from '@/shared/api'

// ── Wire shapes (snake_case, as returned by the WMS API) ────────────────────

interface ApiEnvelope<T> {
  success: boolean
  data: T | null
  error: { code: string; message: string } | null
}

// Non-2xx responses reject via axios. A 200 with success:false / null data would
// otherwise crash the mappers on a null dereference — guard against it. The WMS
// API always pairs an error envelope with a non-2xx status, so this guard only
// covers a contract violation (200 + success:false).
function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }

  return envelope.data
}

interface DestinationResponse {
  id: string
  code: string
  name: string
}

interface BufferBinResponse {
  id: string
  code: string
  destination: DestinationResponse
}

interface ScanBufferProductResponse {
  product_id: string
  qr_code: string
  sku_name: string
  order_external_no: string | null
}

interface ScanBufferResponse {
  buffer_bin: BufferBinResponse
  products: ScanBufferProductResponse[]
  count: number
}

interface ScanDriverResponse {
  dispatch_id: string
  dispatch_code: string
  vehicle_number: string
  driver_name: string
  driver_phone: string | null
  destination: DestinationResponse
  status: OutboundDispatchStatus
  arrived_at: string | null
}

interface ShipResponse {
  products_shipped: number
  outbox_events_created: number
  orders_completed: number
  orders_partially_shipped: number
  dispatch_departed: boolean
  buffer_remaining: number
}

// ── Mappers ─────────────────────────────────────────────────────────────────

function mapDestination(item: DestinationResponse): ShippingDestination {
  return { id: item.id, code: item.code, name: item.name }
}

function mapProduct(item: ScanBufferProductResponse): ShippingBufferProduct {
  return {
    productId: item.product_id,
    qrCode: item.qr_code,
    skuName: item.sku_name,
    orderExternalNo: item.order_external_no,
  }
}

// ── Requests ─────────────────────────────────────────────────────────────────

// Inspect a shipping buffer bin: returns its destination and every READY_TO_SHIP
// product in it. Read-only. count may be 0 (the API does not error on an empty
// buffer here — only /ship does, in bulk mode).
export async function scanBuffer(
  bufferBinId: string,
): Promise<ScanBufferResult> {
  const { data } = await apiClient.post<ApiEnvelope<ScanBufferResponse>>(
    '/shipping/scan-buffer',
    { buffer_bin_id: bufferBinId },
  )

  const result = unwrap(data)

  return {
    bufferBin: {
      id: result.buffer_bin.id,
      code: result.buffer_bin.code,
      destination: mapDestination(result.buffer_bin.destination),
    },
    products: result.products.map(mapProduct),
    count: result.count,
  }
}

// Register a driver's arrival by dispatch code: moves the dispatch
// SCHEDULED → AT_GATE (idempotent if already AT_GATE). Terminal statuses reject.
export async function scanDriver(dispatchCode: string): Promise<DispatchInfo> {
  const { data } = await apiClient.post<ApiEnvelope<ScanDriverResponse>>(
    '/shipping/scan-driver',
    { dispatch_code: dispatchCode },
  )

  const result = unwrap(data)

  return {
    dispatchId: result.dispatch_id,
    dispatchCode: result.dispatch_code,
    vehicleNumber: result.vehicle_number,
    driverName: result.driver_name,
    driverPhone: result.driver_phone,
    destination: mapDestination(result.destination),
    status: result.status,
    arrivedAt: result.arrived_at,
  }
}

export interface ShipParams {
  bufferBinId: string
  dispatchId: string
  // Omit / empty → bulk (whole buffer). Non-empty → spot (exactly these).
  productIds?: string[]
}

// Ship the buffer (bulk) or the listed products (spot): marks them SHIPPED,
// writes shippings + outbox events, advances orders, and departs the dispatch if
// the buffer emptied.
export async function ship(params: ShipParams): Promise<ShipResult> {
  const body: {
    buffer_bin_id: string
    dispatch_id: string
    product_ids?: string[]
  } = {
    buffer_bin_id: params.bufferBinId,
    dispatch_id: params.dispatchId,
  }
  // Only send product_ids in spot mode; bulk omits the field entirely.
  if (params.productIds && params.productIds.length > 0) {
    body.product_ids = params.productIds
  }

  const { data } = await apiClient.post<ApiEnvelope<ShipResponse>>(
    '/shipping/ship',
    body,
  )

  const result = unwrap(data)

  return {
    productsShipped: result.products_shipped,
    outboxEventsCreated: result.outbox_events_created,
    ordersCompleted: result.orders_completed,
    ordersPartiallyShipped: result.orders_partially_shipped,
    dispatchDeparted: result.dispatch_departed,
    bufferRemaining: result.buffer_remaining,
  }
}
