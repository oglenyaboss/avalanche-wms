import type {
  BufferResult,
  ClosedBox,
  ClosedCargoplace,
  CloseCargoplaceSummary,
  ExpectedSku,
  OpenedBox,
  OpenedCargoplace,
  ReceivingProgress,
  RegisteredProduct,
  ResolvedSku,
  ShortageBySku,
} from '@/entities/cargoplace'
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
// non-2xx HTTP status, so axios throws an AxiosError that errors.ts can classify
// (including terminal codes). This guard only covers a contract violation
// (200 + success:false); it throws a plain Error and the flow surfaces the
// generic fallback message rather than terminal-resetting — acceptable because
// that path is unreachable under the documented contract.
function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }

  return envelope.data
}

interface ExpectedSkuResponse {
  sku_id: string
  sku_name: string
  expected_qty: number
}

interface ProgressResponse {
  received_in_cargoplace: number
  expected_in_cargoplace: number
}

interface ScanTableCargoplaceResultResponse {
  cargoplace_id: string
  cargoplace_code: string
  status: 'TABLE_IN_PROGRESS'
  expected_skus: ExpectedSkuResponse[]
  total_expected: number
}

interface ScanBoxResultResponse {
  box_id: string
  box_barcode: string
  status: 'OPEN'
}

interface ScanSkuResultResponse {
  sku_id: string
  sku_name: string
  barcode: string
  message: string
}

interface ScanQrResultResponse {
  product_id: string
  sku_id: string
  sku_name: string
  qr_code: string
  status: 'RECEIVED'
  progress: ProgressResponse
}

interface CloseBoxResultResponse {
  box_id: string
  status: 'CLOSED'
  products_in_box: number
}

interface ScanBufferResultResponse {
  buffer_bin_id: string
  buffer_code: string
  products_placed: number
}

interface ShortageBySkuResponse {
  sku_name: string
  expected: number
  received: number
  shortage: number
}

interface CloseCargoplaceSummaryResponse {
  products_received: number
  products_expected: number
  shortage: number
  shortage_by_sku: ShortageBySkuResponse[] | null
}

interface CloseCargoplaceResultResponse {
  cargoplace_id: string
  status: 'TABLE_CLOSED'
  summary: CloseCargoplaceSummaryResponse
  outbox_events_created: number
}

// ── Mappers ─────────────────────────────────────────────────────────────────

function mapExpectedSku(sku: ExpectedSkuResponse): ExpectedSku {
  return {
    skuId: sku.sku_id,
    skuName: sku.sku_name,
    expectedQty: sku.expected_qty,
  }
}

function mapProgress(progress: ProgressResponse): ReceivingProgress {
  return {
    received: progress.received_in_cargoplace,
    expected: progress.expected_in_cargoplace,
  }
}

function mapShortageBySku(row: ShortageBySkuResponse): ShortageBySku {
  return {
    skuName: row.sku_name,
    expected: row.expected,
    received: row.received,
    shortage: row.shortage,
  }
}

function mapSummary(
  summary: CloseCargoplaceSummaryResponse,
): CloseCargoplaceSummary {
  return {
    productsReceived: summary.products_received,
    productsExpected: summary.products_expected,
    shortage: summary.shortage,
    // shortage_by_sku is omitted (null) when there is no shortage.
    shortageBySku: (summary.shortage_by_sku ?? []).map(mapShortageBySku),
  }
}

// ── Requests ─────────────────────────────────────────────────────────────────

export async function scanTableCargoplace(
  cargoplaceId: string,
): Promise<OpenedCargoplace> {
  const { data } = await apiClient.post<
    ApiEnvelope<ScanTableCargoplaceResultResponse>
  >('/receiving/table/scan-cargoplace', { cargoplace_id: cargoplaceId })

  const result = unwrap(data)

  return {
    cargoplaceId: result.cargoplace_id,
    cargoplaceCode: result.cargoplace_code,
    status: result.status,
    expectedSkus: result.expected_skus.map(mapExpectedSku),
    totalExpected: result.total_expected,
  }
}

export interface ScanBoxInput {
  cargoplaceId: string
  boxBarcode: string
}

export async function scanBox({
  cargoplaceId,
  boxBarcode,
}: ScanBoxInput): Promise<OpenedBox> {
  const { data } = await apiClient.post<ApiEnvelope<ScanBoxResultResponse>>(
    '/receiving/table/scan-box',
    { cargoplace_id: cargoplaceId, box_barcode: boxBarcode },
  )

  const result = unwrap(data)

  return {
    boxId: result.box_id,
    boxBarcode: result.box_barcode,
    status: result.status,
  }
}

export interface ScanSkuInput {
  cargoplaceId: string
  boxId: string
  barcode: string
}

export async function scanSku({
  cargoplaceId,
  boxId,
  barcode,
}: ScanSkuInput): Promise<ResolvedSku> {
  const { data } = await apiClient.post<ApiEnvelope<ScanSkuResultResponse>>(
    '/receiving/table/scan-sku',
    { cargoplace_id: cargoplaceId, box_id: boxId, barcode },
  )

  const result = unwrap(data)

  return {
    skuId: result.sku_id,
    skuName: result.sku_name,
    barcode: result.barcode,
    message: result.message,
  }
}

export interface ScanQrInput {
  cargoplaceId: string
  boxId: string
  skuId: string
  qrCode: string
}

export async function scanQr({
  cargoplaceId,
  boxId,
  skuId,
  qrCode,
}: ScanQrInput): Promise<RegisteredProduct> {
  const { data } = await apiClient.post<ApiEnvelope<ScanQrResultResponse>>(
    '/receiving/table/scan-qr',
    {
      cargoplace_id: cargoplaceId,
      box_id: boxId,
      sku_id: skuId,
      qr_code: qrCode,
    },
  )

  const result = unwrap(data)

  return {
    productId: result.product_id,
    skuId: result.sku_id,
    skuName: result.sku_name,
    qrCode: result.qr_code,
    status: result.status,
    progress: mapProgress(result.progress),
  }
}

export async function closeBox(boxId: string): Promise<ClosedBox> {
  const { data } = await apiClient.post<ApiEnvelope<CloseBoxResultResponse>>(
    '/receiving/table/close-box',
    { box_id: boxId },
  )

  const result = unwrap(data)

  return {
    boxId: result.box_id,
    status: result.status,
    productsInBox: result.products_in_box,
  }
}

export interface ScanBufferInput {
  cargoplaceId: string
  bufferBinId: string
}

export async function scanBuffer({
  cargoplaceId,
  bufferBinId,
}: ScanBufferInput): Promise<BufferResult> {
  const { data } = await apiClient.post<ApiEnvelope<ScanBufferResultResponse>>(
    '/receiving/table/scan-buffer',
    { cargoplace_id: cargoplaceId, buffer_bin_id: bufferBinId },
  )

  const result = unwrap(data)

  return {
    bufferBinId: result.buffer_bin_id,
    bufferCode: result.buffer_code,
    productsPlaced: result.products_placed,
  }
}

export async function closeCargoplace(
  cargoplaceId: string,
): Promise<ClosedCargoplace> {
  const { data } = await apiClient.post<
    ApiEnvelope<CloseCargoplaceResultResponse>
  >('/receiving/table/close-cargoplace', { cargoplace_id: cargoplaceId })

  const result = unwrap(data)

  return {
    cargoplaceId: result.cargoplace_id,
    status: result.status,
    summary: mapSummary(result.summary),
    outboxEventsCreated: result.outbox_events_created,
  }
}
