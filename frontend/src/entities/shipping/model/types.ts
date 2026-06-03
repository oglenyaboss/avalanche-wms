// Domain model for OUTBOUND shipping (отгрузка): the final stage where an
// arrived truck loads a store's READY_TO_SHIP products out of its shipping
// buffer. NOTE: this is the outbound counterpart of the inbound entities/shipment
// slice (приёмка) — do not confuse the two. API responses are snake_case; these
// camelCase types are the normalized shape the feature layer works with. Mapping
// lives in the feature api layer.

// The outbound dispatch (рейс) lifecycle, as stored in
// wms_inventory.outbound_dispatches and returned by /shipping/scan-driver.
export type OutboundDispatchStatus =
  | 'SCHEDULED'
  | 'AT_GATE'
  | 'DEPARTED'
  | 'CANCELLED'

// A destination (store) as embedded in the scan-buffer / scan-driver responses —
// the trimmed {id, code, name} shape, not the full destinations DTO.
export interface ShippingDestination {
  id: string
  code: string
  name: string
}

// A SHIPPING_BUFFER bin tied to one destination. The buffer's destination is the
// zone the loaded truck must match.
export interface ShippingBufferBin {
  id: string
  code: string
  destination: ShippingDestination
}

// One READY_TO_SHIP product sitting in the buffer. orderExternalNo is null for a
// product not yet tied to a customer order.
export interface ShippingBufferProduct {
  productId: string
  qrCode: string
  skuName: string
  orderExternalNo: string | null
}

// scan-buffer result: the buffer's metadata plus every READY_TO_SHIP product in
// it. count mirrors products.length (server-computed).
export interface ScanBufferResult {
  bufferBin: ShippingBufferBin
  products: ShippingBufferProduct[]
  count: number
}

// scan-driver result: the dispatch after it was moved SCHEDULED → AT_GATE (or
// read back when it was already AT_GATE). destination is the zone the truck is
// assigned to — the UI compares it against the buffer's destination.
export interface DispatchInfo {
  dispatchId: string
  dispatchCode: string
  vehicleNumber: string
  driverName: string
  driverPhone: string | null
  destination: ShippingDestination
  status: OutboundDispatchStatus
  arrivedAt: string | null
}

// ship result: how the buffer and orders moved after a bulk or spot ship.
// dispatchDeparted is true when the buffer emptied and the dispatch became
// DEPARTED; bufferRemaining is the count still READY_TO_SHIP in the buffer's zone.
export interface ShipResult {
  productsShipped: number
  outboxEventsCreated: number
  ordersCompleted: number
  ordersPartiallyShipped: number
  dispatchDeparted: boolean
  bufferRemaining: number
}
