// Domain model for assembly (сборка): picking ALLOCATED products out of storage
// bins for a destination's orders and moving the operator's cart into a shipping
// buffer bin in one move.
//
// API responses are snake_case; these camelCase types are the normalized shape
// the feature layer works with. Mapping lives in the feature api layer.

// A store the picker assembles orders for. address may be null for stores that
// have no address on file. warehouseId is the source warehouse the picker works.
export interface Destination {
  destinationId: string
  code: string
  name: string
  address: string | null
  warehouseId: number
}

// One SKU an order could not be fully allocated for: how many units are still
// missing after allocation ran.
export interface MissingSku {
  skuId: string
  skuName: string
  missingQty: number
}

// An order that could not be fully allocated — it lists the SKUs that ran short.
export interface InsufficientOrder {
  orderId: string
  missingSkus: MissingSku[]
}

// allocate result: how many orders/products got reserved, plus the orders that
// came up short. allocate always succeeds (HTTP 200) even on shortage; the
// shortage is reported here, not as an error.
export interface AllocationResult {
  allocatedOrders: number
  allocatedProducts: number
  insufficientOrders: InsufficientOrder[]
}

// A PENDING pick task: one ALLOCATED product to fetch from its storage bin. The
// qr_code ↔ product_id pairing is what lets the UI resolve a scanned QR to the
// product_id the pick endpoint requires.
export interface AssemblyTask {
  taskId: string
  productId: string
  qrCode: string
  skuName: string
  fromBinCode: string
  fromBinSection: string
  orderNo: string
}

// pick result: marks ALLOCATED → ASSEMBLED. cartSize is the operator's running
// cart count from the server (the count of products they are carrying).
export interface PickResult {
  productId: string
  cartSize: number
}

// A product picked into the assembly cart — the client-side running list of
// items the operator is carrying to the shipping buffer ("Товары к сборке").
export interface PickedProduct {
  productId: string
  skuName: string
  qrCode: string
  fromBinCode: string
  orderNo: string
}

// scan-shipping-buffer result: the whole cart moved into the buffer bin in one
// call. productsPlaced is how many cart items landed; ordersAssembled is how
// many orders became fully assembled as a result.
export interface ShippingBufferResult {
  bufferBinId: string
  productsPlaced: number
  ordersAssembled: number
}
