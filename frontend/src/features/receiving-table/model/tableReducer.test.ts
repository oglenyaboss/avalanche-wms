import { describe, expect, it } from 'vitest'

import type {
  CloseCargoplaceSummary,
  OpenedBox,
  OpenedCargoplace,
  RegisteredProduct,
  ResolvedSku,
} from '@/entities/cargoplace'

import {
  initialTableState,
  tableReducer,
  type TableState,
} from './tableReducer'

const openedCargoplace: OpenedCargoplace = {
  cargoplaceId: 'cp-1',
  cargoplaceCode: 'CP-TABLE-001',
  status: 'TABLE_IN_PROGRESS',
  expectedSkus: [
    { skuId: 'sku-1', skuName: 'Кроссовки', expectedQty: 2 },
    { skuId: 'sku-2', skuName: 'Футболка', expectedQty: 1 },
  ],
  totalExpected: 3,
}

const openedBox: OpenedBox = {
  boxId: 'box-1',
  boxBarcode: 'BOX-1',
  status: 'OPEN',
}

const resolvedSku: ResolvedSku = {
  skuId: 'sku-1',
  skuName: 'Кроссовки',
  barcode: '4600000000011',
  message: 'Наклейте QR на товар',
}

function registered(
  overrides: Partial<RegisteredProduct> = {},
): RegisteredProduct {
  return {
    productId: 'p-1',
    skuId: 'sku-1',
    skuName: 'Кроссовки',
    qrCode: 'QR-1',
    status: 'RECEIVED',
    progress: { received: 1, expected: 3 },
    ...overrides,
  }
}

// Walk the happy path up to an open box ready for product scanning.
function openBox(): TableState {
  const afterCargoplace = tableReducer(initialTableState, {
    type: 'CARGOPLACE_OPENED',
    result: openedCargoplace,
  })
  return tableReducer(afterCargoplace, { type: 'BOX_OPENED', result: openedBox })
}

describe('tableReducer / CARGOPLACE_OPENED', () => {
  it('enters the box phase with a zeroed progress and the expected manifest', () => {
    const state = tableReducer(initialTableState, {
      type: 'CARGOPLACE_OPENED',
      result: openedCargoplace,
    })

    expect(state.phase).toBe('box')
    expect(state.cargoplace?.cargoplaceCode).toBe('CP-TABLE-001')
    expect(state.cargoplace?.expectedSkus).toHaveLength(2)
    expect(state.cargoplace?.progress).toEqual({ received: 0, expected: 3 })
    expect(state.box).toBeNull()
  })

  it('clears any prior success/error message', () => {
    const dirty: TableState = {
      ...initialTableState,
      errorMessage: 'old',
      successMessage: 'old',
    }
    const state = tableReducer(dirty, {
      type: 'CARGOPLACE_OPENED',
      result: openedCargoplace,
    })

    expect(state.errorMessage).toBeNull()
    expect(state.successMessage).toBeNull()
  })
})

describe('tableReducer / box and product flow', () => {
  it('BOX_OPENED moves to the product phase at the barcode sub-step', () => {
    const state = openBox()

    expect(state.phase).toBe('product')
    expect(state.productStep).toBe('barcode')
    expect(state.box?.boxId).toBe('box-1')
  })

  it('BOX_OPENED is a no-op without a cargoplace', () => {
    const state = tableReducer(initialTableState, {
      type: 'BOX_OPENED',
      result: openedBox,
    })

    expect(state).toBe(initialTableState)
  })

  it('SKU_RESOLVED advances to the qr sub-step and stores the SKU', () => {
    const state = tableReducer(openBox(), {
      type: 'SKU_RESOLVED',
      result: resolvedSku,
    })

    expect(state.productStep).toBe('qr')
    expect(state.resolvedSku?.skuName).toBe('Кроссовки')
  })

  it('PRODUCT_REGISTERED appends the product, updates progress, returns to barcode', () => {
    const afterSku = tableReducer(openBox(), {
      type: 'SKU_RESOLVED',
      result: resolvedSku,
    })
    const state = tableReducer(afterSku, {
      type: 'PRODUCT_REGISTERED',
      result: registered({ progress: { received: 1, expected: 3 } }),
    })

    expect(state.productStep).toBe('barcode')
    expect(state.resolvedSku).toBeNull()
    expect(state.scannedProducts).toHaveLength(1)
    expect(state.scannedProducts[0]?.qrCode).toBe('QR-1')
    expect(state.cargoplace?.progress.received).toBe(1)
  })

  it('keeps accumulating scanned products across multiple QR scans', () => {
    let state = tableReducer(openBox(), {
      type: 'SKU_RESOLVED',
      result: resolvedSku,
    })
    state = tableReducer(state, {
      type: 'PRODUCT_REGISTERED',
      result: registered({ productId: 'p-1', qrCode: 'QR-1' }),
    })
    state = tableReducer(state, { type: 'SKU_RESOLVED', result: resolvedSku })
    state = tableReducer(state, {
      type: 'PRODUCT_REGISTERED',
      result: registered({
        productId: 'p-2',
        qrCode: 'QR-2',
        progress: { received: 2, expected: 3 },
      }),
    })

    expect(state.scannedProducts.map((p) => p.qrCode)).toEqual(['QR-1', 'QR-2'])
    expect(state.cargoplace?.progress.received).toBe(2)
  })

  it('BOX_CLOSED returns to the box phase and forgets the box', () => {
    const state = tableReducer(openBox(), { type: 'BOX_CLOSED' })

    expect(state.phase).toBe('box')
    expect(state.box).toBeNull()
    expect(state.confirmCloseBoxOpen).toBe(false)
  })
})

describe('tableReducer / buffer and finish', () => {
  it('GO_TO_BUFFER enters the buffer phase with no placement or summary yet', () => {
    const state = tableReducer(openBox(), { type: 'GO_TO_BUFFER' })

    expect(state.phase).toBe('buffer')
    expect(state.bufferPlaced).toBeNull()
    expect(state.summary).toBeNull()
  })

  it('BUFFER_SCANNED records how many products were placed', () => {
    const atBuffer = tableReducer(openBox(), { type: 'GO_TO_BUFFER' })
    const state = tableReducer(atBuffer, {
      type: 'BUFFER_SCANNED',
      productsPlaced: 5,
    })

    expect(state.bufferPlaced).toBe(5)
    expect(state.summary).toBeNull()
  })

  it('CARGOPLACE_CLOSED stores the summary without leaving the buffer phase', () => {
    const summary: CloseCargoplaceSummary = {
      productsReceived: 2,
      productsExpected: 3,
      shortage: 1,
      shortageBySku: [
        { skuName: 'Кроссовки', expected: 2, received: 1, shortage: 1 },
      ],
    }
    const atBuffer = tableReducer(openBox(), { type: 'GO_TO_BUFFER' })
    const state = tableReducer(atBuffer, {
      type: 'CARGOPLACE_CLOSED',
      summary,
    })

    expect(state.phase).toBe('buffer')
    expect(state.summary?.shortage).toBe(1)
  })

  it('FINISH resets and reports the shortage summary message', () => {
    const summary: CloseCargoplaceSummary = {
      productsReceived: 2,
      productsExpected: 3,
      shortage: 1,
      shortageBySku: [],
    }
    let state = tableReducer(openBox(), { type: 'GO_TO_BUFFER' })
    state = tableReducer(state, { type: 'CARGOPLACE_CLOSED', summary })
    state = tableReducer(state, { type: 'FINISH' })

    expect(state.phase).toBe('cargoplace')
    expect(state.cargoplace).toBeNull()
    expect(state.successMessage).toContain('обработано')
    expect(state.successMessage).toContain('недостача 1')
  })

  it('FINISH reports a clean summary when there is no shortage', () => {
    const summary: CloseCargoplaceSummary = {
      productsReceived: 3,
      productsExpected: 3,
      shortage: 0,
      shortageBySku: [],
    }
    let state = tableReducer(openBox(), { type: 'GO_TO_BUFFER' })
    state = tableReducer(state, { type: 'CARGOPLACE_CLOSED', summary })
    state = tableReducer(state, { type: 'FINISH' })

    expect(state.successMessage).toContain('Принято 3 из 3')
    expect(state.successMessage).not.toContain('недостача')
  })
})

describe('tableReducer / errors & dialogs', () => {
  it('SHOW_ERROR keeps the current phase and shipment context', () => {
    const state = tableReducer(openBox(), {
      type: 'SHOW_ERROR',
      message: 'duplicate QR',
    })

    expect(state.phase).toBe('product')
    expect(state.cargoplace).not.toBeNull()
    expect(state.errorMessage).toBe('duplicate QR')
  })

  it('SHOW_TERMINAL_ERROR drops back to the cargoplace scan', () => {
    const state = tableReducer(openBox(), {
      type: 'SHOW_TERMINAL_ERROR',
      message: 'cargoplace gone',
    })

    expect(state.phase).toBe('cargoplace')
    expect(state.cargoplace).toBeNull()
    expect(state.errorMessage).toBe('cargoplace gone')
  })

  it('BOX_LOST drops back to the box scan but keeps the cargoplace and products', () => {
    const withProduct = tableReducer(
      tableReducer(openBox(), { type: 'SKU_RESOLVED', result: resolvedSku }),
      { type: 'PRODUCT_REGISTERED', result: registered() },
    )
    const state = tableReducer(withProduct, {
      type: 'BOX_LOST',
      message: 'box closed',
    })

    expect(state.phase).toBe('box')
    expect(state.box).toBeNull()
    expect(state.cargoplace).not.toBeNull()
    expect(state.scannedProducts).toHaveLength(1)
    expect(state.errorMessage).toBe('box closed')
  })

  it('BOX_LOST without a cargoplace falls back to a full reset', () => {
    const state = tableReducer(initialTableState, {
      type: 'BOX_LOST',
      message: 'lost',
    })

    expect(state.phase).toBe('cargoplace')
    expect(state.errorMessage).toBe('lost')
  })

  it('toggles both confirm dialogs and dismisses errors', () => {
    expect(
      tableReducer(openBox(), { type: 'OPEN_CONFIRM_CLOSE_BOX' })
        .confirmCloseBoxOpen,
    ).toBe(true)
    expect(
      tableReducer(openBox(), { type: 'OPEN_CONFIRM_ACCEPT_CARGOPLACE' })
        .confirmAcceptCargoplaceOpen,
    ).toBe(true)

    const withError = tableReducer(openBox(), {
      type: 'SHOW_ERROR',
      message: 'x',
    })
    expect(
      tableReducer(withError, { type: 'DISMISS_ERROR' }).errorMessage,
    ).toBeNull()
  })

  it('SHOW_ERROR closes any open confirm dialog', () => {
    const withDialog = tableReducer(openBox(), {
      type: 'OPEN_CONFIRM_CLOSE_BOX',
    })
    const state = tableReducer(withDialog, {
      type: 'SHOW_ERROR',
      message: 'boom',
    })

    expect(state.confirmCloseBoxOpen).toBe(false)
  })
})
