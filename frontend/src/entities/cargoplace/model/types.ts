// Domain model for table receiving (приёмка на столах): opening a cargoplace,
// scanning boxes, resolving SKUs by barcode, registering products by QR, moving
// them to a buffer bin and closing the cargoplace.
//
// API responses are snake_case; these camelCase types are the normalized shape
// the feature layer works with. Mapping lives in the feature api layer.

export type BoxStatus = 'OPEN' | 'CLOSED'

export interface ExpectedSku {
  skuId: string
  skuName: string
  expectedQty: number
}

// Running progress for a cargoplace: how many products have been registered
// against how many were expected.
export interface ReceivingProgress {
  received: number
  expected: number
}

export interface OpenedCargoplace {
  cargoplaceId: string
  cargoplaceCode: string
  status: 'TABLE_IN_PROGRESS'
  expectedSkus: ExpectedSku[]
  totalExpected: number
}

export interface OpenedBox {
  boxId: string
  boxBarcode: string
  status: BoxStatus
}

// scan-sku resolves a barcode to a SKU but does NOT create a product. The
// backend returns a hardcoded prompt ("Наклейте QR на товар").
export interface ResolvedSku {
  skuId: string
  skuName: string
  barcode: string
  message: string
}

// scan-qr creates the product row and returns updated cargoplace progress.
export interface RegisteredProduct {
  productId: string
  skuId: string
  skuName: string
  qrCode: string
  status: 'RECEIVED'
  progress: ReceivingProgress
}

export interface ClosedBox {
  boxId: string
  status: 'CLOSED'
  productsInBox: number
}

export interface BufferResult {
  bufferBinId: string
  bufferCode: string
  productsPlaced: number
}

export interface ShortageBySku {
  skuName: string
  expected: number
  received: number
  shortage: number
}

export interface CloseCargoplaceSummary {
  productsReceived: number
  productsExpected: number
  shortage: number
  shortageBySku: ShortageBySku[]
}

export interface ClosedCargoplace {
  cargoplaceId: string
  status: 'TABLE_CLOSED'
  summary: CloseCargoplaceSummary
  outboxEventsCreated: number
}

// A product registered during the current session, kept for the running list
// the operator sees ("Товары на раскладку").
export interface ScannedProduct {
  productId: string
  skuId: string
  skuName: string
  qrCode: string
}
