// Domain model for inbound shipments and their cargoplaces (приёмка на воротах).
// API responses are snake_case; these camelCase types are the normalized shape
// the feature layer works with. Mapping lives in the feature api layer.

export type ShipmentStatus = 'CREATED' | 'GATE_IN_PROGRESS' | 'GATE_CLOSED'

export type CargoplaceStatus =
  | 'EXPECTED'
  | 'RECEIVED_AT_GATE'
  | 'NOT_RECEIVED'
  | 'TABLE_IN_PROGRESS'
  | 'TABLE_CLOSED'

export interface Cargoplace {
  id: string
  code: string
  status: CargoplaceStatus
}

export interface GateProgress {
  total: number
  received: number
  remaining: number
  notReceived: number
}

export interface OpenedShipment {
  shipmentId: string
  ttnCode: string
  status: ShipmentStatus
  cargoplaces: Cargoplace[]
  totalCargoplaces: number
  receivedCargoplaces: number
}

export interface CargoplaceScanResult {
  cargoplace: Cargoplace
  receivedAtGate: string
  progress: GateProgress
}

export interface ShipmentCloseResult {
  shipmentId: string
  status: ShipmentStatus
  summary: GateProgress
}
