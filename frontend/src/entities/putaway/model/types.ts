// Domain model for putaway (раскладка): moving RECEIVED products out of a
// receiving buffer bin into storage bins on the mezzanine.
//
// API responses are snake_case; these camelCase types are the normalized shape
// the feature layer works with. Mapping lives in the feature api layer.

// A product sitting in the receiving buffer, available to pick for putaway.
// The buffer scan returns these RECEIVED products; the qr_code ↔ product_id
// pairing is what lets the UI resolve a scanned QR to the product_id the
// scan-product / scan-storage-bin endpoints require.
export interface BufferProduct {
  productId: string
  skuName: string
  qrCode: string
  status: 'RECEIVED'
}

// scan-buffer result: the buffer bin plus the RECEIVED products it holds.
export interface BufferScan {
  bufferBinId: string
  bufferCode: string
  products: BufferProduct[]
  totalProducts: number
}

// scan-product result: a server-validated product plus the buffer's RECEIVED
// inventory count. cartSize is the DB count of RECEIVED products in the buffer
// (read-only); it does NOT decrement as the operator picks, so the UI tracks
// its own picked count and treats cartSize as informational.
export interface PickedProductResult {
  productId: string
  skuName: string
  qrCode: string
  cartSize: number
}

// A product picked into the putaway cart — the client-side running list of
// items the operator is carrying to the mezzanine ("Товары на раскладку").
export interface PickedProduct {
  productId: string
  skuName: string
  qrCode: string
}

// scan-storage-bin result: products moved RECEIVED → STORED into a storage bin.
// storageBinCode (e.g. "M2-A-03") is only known from this response — there is
// no separate "resolve bin" endpoint.
export interface StoragePlacement {
  storageBinId: string
  storageBinCode: string
  productsPlaced: number
  outboxEventsCreated: number
}
