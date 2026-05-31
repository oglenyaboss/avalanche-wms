import type {
  Cargoplace,
  CargoplaceScanResult,
  CargoplaceStatus,
  GateProgress,
  OpenedShipment,
  ShipmentCloseResult,
  ShipmentStatus,
} from '@/entities/shipment'
import { apiClient } from '@/shared/api'

// ── Wire shapes (snake_case, as returned by the WMS API) ────────────────────

interface ApiEnvelope<T> {
  success: boolean
  data: T | null
  error: { code: string; message: string } | null
}

// Non-2xx responses reject via axios. A 200 with success:false / null data
// would otherwise crash the mappers on a null dereference — guard against it.
function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }

  return envelope.data
}

interface CargoplaceViewResponse {
  cargoplace_id: string
  cargoplace_code: string
  status: CargoplaceStatus
}

interface GateProgressResponse {
  total: number
  received: number
  remaining: number
  not_received: number
}

interface ScanTtnResultResponse {
  shipment_id: string
  ttn_code: string
  status: ShipmentStatus
  cargoplaces: CargoplaceViewResponse[]
  total_cargoplaces: number
  received_cargoplaces: number
}

interface ScanGateCargoplaceResultResponse {
  cargoplace_id: string
  cargoplace_code: string
  status: CargoplaceStatus
  received_at_gate_at: string
  progress: GateProgressResponse
}

interface AcceptShipmentResultResponse {
  shipment_id: string
  status: ShipmentStatus
  summary: GateProgressResponse
}

// ── Mappers ─────────────────────────────────────────────────────────────────

function mapCargoplace(view: CargoplaceViewResponse): Cargoplace {
  return {
    id: view.cargoplace_id,
    code: view.cargoplace_code,
    status: view.status,
  }
}

function mapProgress(progress: GateProgressResponse): GateProgress {
  return {
    total: progress.total,
    received: progress.received,
    remaining: progress.remaining,
    notReceived: progress.not_received,
  }
}

function mapOpenedShipment(result: ScanTtnResultResponse): OpenedShipment {
  return {
    shipmentId: result.shipment_id,
    ttnCode: result.ttn_code,
    status: result.status,
    cargoplaces: result.cargoplaces.map(mapCargoplace),
    totalCargoplaces: result.total_cargoplaces,
    receivedCargoplaces: result.received_cargoplaces,
  }
}

// ── Requests ─────────────────────────────────────────────────────────────────

export async function scanTtn(ttnCode: string): Promise<OpenedShipment> {
  const { data } = await apiClient.post<ApiEnvelope<ScanTtnResultResponse>>(
    '/receiving/gate/scan-ttn',
    { ttn_code: ttnCode },
  )

  return mapOpenedShipment(unwrap(data))
}

export interface ScanGateCargoplaceInput {
  shipmentId: string
  cargoplaceCode: string
}

export async function scanGateCargoplace({
  shipmentId,
  cargoplaceCode,
}: ScanGateCargoplaceInput): Promise<CargoplaceScanResult> {
  const { data } = await apiClient.post<
    ApiEnvelope<ScanGateCargoplaceResultResponse>
  >('/receiving/gate/scan-cargoplace', {
    shipment_id: shipmentId,
    cargoplace_code: cargoplaceCode,
  })

  const result = unwrap(data)

  return {
    cargoplace: {
      id: result.cargoplace_id,
      code: result.cargoplace_code,
      status: result.status,
    },
    receivedAtGate: result.received_at_gate_at,
    progress: mapProgress(result.progress),
  }
}

export async function acceptShipment(
  shipmentId: string,
): Promise<ShipmentCloseResult> {
  const { data } = await apiClient.post<
    ApiEnvelope<AcceptShipmentResultResponse>
  >('/receiving/gate/accept-shipment', { shipment_id: shipmentId })

  const result = unwrap(data)

  return {
    shipmentId: result.shipment_id,
    status: result.status,
    summary: mapProgress(result.summary),
  }
}
