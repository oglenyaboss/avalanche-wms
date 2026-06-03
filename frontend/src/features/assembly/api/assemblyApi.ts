import type {
  AllocationResult,
  AssemblyTask,
  Destination,
  InsufficientOrder,
  PickResult,
  ShippingBufferResult,
} from '@/entities/assembly'
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

interface DestinationResponse {
  destination_id: string
  code: string
  name: string
  address: string | null
  warehouse_id: number
}

interface GetDestinationsResponse {
  destinations: DestinationResponse[]
}

interface MissingSkuResponse {
  sku_id: string
  sku_name: string
  missing_qty: number
}

interface InsufficientOrderResponse {
  order_id: string
  missing_skus: MissingSkuResponse[]
}

interface AllocateResponse {
  allocated_orders: number
  allocated_products: number
  insufficient_orders: InsufficientOrderResponse[]
}

interface AssemblyTaskResponse {
  task_id: string
  product_id: string
  qr_code: string
  sku_name: string
  from_bin_code: string
  from_bin_section: string
  order_no: string
}

interface GetTasksResponse {
  tasks: AssemblyTaskResponse[]
}

interface PickResponse {
  product_id: string
  cart_size: number
}

interface ScanShippingBufferResponse {
  buffer_bin_id: string
  products_placed: number
  orders_assembled: number
}

// ── Mappers ─────────────────────────────────────────────────────────────────

function mapDestination(item: DestinationResponse): Destination {
  return {
    destinationId: item.destination_id,
    code: item.code,
    name: item.name,
    address: item.address,
    warehouseId: item.warehouse_id,
  }
}

function mapInsufficientOrder(
  item: InsufficientOrderResponse,
): InsufficientOrder {
  return {
    orderId: item.order_id,
    missingSkus: item.missing_skus.map((sku) => ({
      skuId: sku.sku_id,
      skuName: sku.sku_name,
      missingQty: sku.missing_qty,
    })),
  }
}

function mapTask(item: AssemblyTaskResponse): AssemblyTask {
  return {
    taskId: item.task_id,
    productId: item.product_id,
    qrCode: item.qr_code,
    skuName: item.sku_name,
    fromBinCode: item.from_bin_code,
    fromBinSection: item.from_bin_section,
    orderNo: item.order_no,
  }
}

// ── Requests ─────────────────────────────────────────────────────────────────

export async function getDestinations(): Promise<Destination[]> {
  const { data } =
    await apiClient.get<ApiEnvelope<GetDestinationsResponse>>('/destinations')

  const result = unwrap(data)

  return result.destinations.map(mapDestination)
}

export async function allocate(
  destinationId: string,
): Promise<AllocationResult> {
  const { data } = await apiClient.post<ApiEnvelope<AllocateResponse>>(
    '/assembly/allocate',
    { destination_id: destinationId },
  )

  const result = unwrap(data)

  return {
    allocatedOrders: result.allocated_orders,
    allocatedProducts: result.allocated_products,
    insufficientOrders: result.insufficient_orders.map(mapInsufficientOrder),
  }
}

export async function getTasks(
  destinationId: string,
): Promise<AssemblyTask[]> {
  const { data } = await apiClient.get<ApiEnvelope<GetTasksResponse>>(
    `/assembly/tasks?destination_id=${encodeURIComponent(destinationId)}`,
  )

  const result = unwrap(data)

  return result.tasks.map(mapTask)
}

export async function pick(productId: string): Promise<PickResult> {
  const { data } = await apiClient.post<ApiEnvelope<PickResponse>>(
    '/assembly/pick',
    { product_id: productId },
  )

  const result = unwrap(data)

  return {
    productId: result.product_id,
    cartSize: result.cart_size,
  }
}

export async function scanShippingBuffer(
  bufferBinId: string,
): Promise<ShippingBufferResult> {
  const { data } = await apiClient.post<
    ApiEnvelope<ScanShippingBufferResponse>
  >('/assembly/scan-shipping-buffer', { buffer_bin_id: bufferBinId })

  const result = unwrap(data)

  return {
    bufferBinId: result.buffer_bin_id,
    productsPlaced: result.products_placed,
    ordersAssembled: result.orders_assembled,
  }
}
