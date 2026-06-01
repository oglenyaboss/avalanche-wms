import { describe, expect, it } from 'vitest'

import type {
  DispatchInfo,
  ScanBufferResult,
  ShipResult,
  ShippingBufferProduct,
} from '@/entities/shipping'

import {
  initialShippingState,
  shippingReducer,
  type ShippingState,
} from './shippingReducer'

const DEST_5 = { id: 'dest-5', code: 'SHOP-5', name: 'Магазин №5' }
const DEST_7 = { id: 'dest-7', code: 'SHOP-7', name: 'Магазин №7' }

const products: ShippingBufferProduct[] = [
  {
    productId: 'p-1',
    qrCode: 'QR-1',
    skuName: 'Кроссовки',
    orderExternalNo: 'ORD-100',
  },
  {
    productId: 'p-2',
    qrCode: 'QR-2',
    skuName: 'Футболка',
    orderExternalNo: null,
  },
]

function scanBufferResult(
  overrides: Partial<ScanBufferResult> = {},
): ScanBufferResult {
  return {
    bufferBin: { id: 'buf-5', code: 'BIN-SHIP-5', destination: DEST_5 },
    products,
    count: products.length,
    ...overrides,
  }
}

function dispatchInfo(overrides: Partial<DispatchInfo> = {}): DispatchInfo {
  return {
    dispatchId: 'dsp-1',
    dispatchCode: 'DSP-2026-0421-001',
    vehicleNumber: 'A123BC777',
    driverName: 'Иван Петров',
    driverPhone: '+7 (999) 555-00-11',
    destination: DEST_5,
    status: 'AT_GATE',
    arrivedAt: '2026-06-01T09:55:00Z',
    ...overrides,
  }
}

function shipResult(overrides: Partial<ShipResult> = {}): ShipResult {
  return {
    productsShipped: 2,
    outboxEventsCreated: 2,
    ordersCompleted: 1,
    ordersPartiallyShipped: 0,
    dispatchDeparted: true,
    bufferRemaining: 0,
    ...overrides,
  }
}

// Drive the reducer to the ship phase (buffer scanned + driver scanned).
function shipPhaseState(
  driverOverrides: Partial<DispatchInfo> = {},
): ShippingState {
  const afterBuffer = shippingReducer(initialShippingState, {
    type: 'BUFFER_SCANNED',
    result: scanBufferResult(),
  })
  return shippingReducer(afterBuffer, {
    type: 'DRIVER_SCANNED',
    result: dispatchInfo(driverOverrides),
  })
}

describe('shippingReducer', () => {
  describe('BUFFER_SCANNED', () => {
    it('advances to the driver phase with the buffer and its products', () => {
      const next = shippingReducer(initialShippingState, {
        type: 'BUFFER_SCANNED',
        result: scanBufferResult(),
      })

      expect(next.phase).toBe('driver')
      expect(next.bufferBin?.code).toBe('BIN-SHIP-5')
      expect(next.products).toHaveLength(2)
      expect(next.emptyBufferMessage).toBeNull()
    })

    it('stays on the buffer phase with an info note when the buffer is empty', () => {
      const next = shippingReducer(initialShippingState, {
        type: 'BUFFER_SCANNED',
        result: scanBufferResult({ products: [], count: 0 }),
      })

      expect(next.phase).toBe('buffer')
      expect(next.bufferBin).toBeNull()
      expect(next.emptyBufferMessage).toContain('Магазин №5')
    })

    it('ignores a buffer scan outside the buffer phase', () => {
      const state = shipPhaseState()
      const next = shippingReducer(state, {
        type: 'BUFFER_SCANNED',
        result: scanBufferResult(),
      })

      expect(next).toBe(state)
    })
  })

  describe('DRIVER_SCANNED', () => {
    it('advances to the ship phase with matching destinations', () => {
      const next = shipPhaseState()

      expect(next.phase).toBe('ship')
      expect(next.dispatch?.vehicleNumber).toBe('A123BC777')
      expect(next.destinationMismatch).toBe(false)
    })

    it('flags a destination mismatch when the truck is for another zone', () => {
      const next = shipPhaseState({ destination: DEST_7 })

      expect(next.phase).toBe('ship')
      expect(next.destinationMismatch).toBe(true)
    })
  })

  describe('spot selection', () => {
    it('toggles a product in the buffer on and off', () => {
      const base = shipPhaseState()
      const added = shippingReducer(base, {
        type: 'TOGGLE_PRODUCT',
        productId: 'p-1',
      })
      expect(added.selectedProductIds).toEqual(['p-1'])

      const removed = shippingReducer(added, {
        type: 'TOGGLE_PRODUCT',
        productId: 'p-1',
      })
      expect(removed.selectedProductIds).toEqual([])
    })

    it('ignores toggling a product not in the buffer', () => {
      const base = shipPhaseState()
      const next = shippingReducer(base, {
        type: 'TOGGLE_PRODUCT',
        productId: 'ghost',
      })
      expect(next.selectedProductIds).toEqual([])
    })

    it('adds a scanned product once (idempotent, no duplicate)', () => {
      const base = shipPhaseState()
      const once = shippingReducer(base, {
        type: 'SELECT_PRODUCT',
        productId: 'p-2',
      })
      const twice = shippingReducer(once, {
        type: 'SELECT_PRODUCT',
        productId: 'p-2',
      })
      expect(twice.selectedProductIds).toEqual(['p-2'])
    })

    it('clears the whole selection', () => {
      const base = shippingReducer(shipPhaseState(), {
        type: 'TOGGLE_PRODUCT',
        productId: 'p-1',
      })
      const cleared = shippingReducer(base, { type: 'CLEAR_SELECTION' })
      expect(cleared.selectedProductIds).toEqual([])
    })
  })

  describe('SHIPPED', () => {
    it('moves to the done phase when the dispatch departed', () => {
      const next = shippingReducer(shipPhaseState(), {
        type: 'SHIPPED',
        result: shipResult({ dispatchDeparted: true, bufferRemaining: 0 }),
      })

      expect(next.phase).toBe('done')
      expect(next.lastShipResult?.productsShipped).toBe(2)
    })

    it('stays on ship with a partial note when items remain', () => {
      const withSelection = shippingReducer(shipPhaseState(), {
        type: 'TOGGLE_PRODUCT',
        productId: 'p-1',
      })
      const next = shippingReducer(withSelection, {
        type: 'SHIPPED',
        result: shipResult({
          productsShipped: 1,
          dispatchDeparted: false,
          ordersCompleted: 0,
          ordersPartiallyShipped: 1,
          bufferRemaining: 1,
        }),
      })

      expect(next.phase).toBe('ship')
      expect(next.partialMessage).toContain('Осталось в буфере: 1')
      expect(next.selectedProductIds).toEqual([])
    })
  })

  describe('BUFFER_REFRESHED', () => {
    it('updates the products and prunes a stale selection', () => {
      const withSelection = shippingReducer(shipPhaseState(), {
        type: 'TOGGLE_PRODUCT',
        productId: 'p-1',
      })
      // p-1 was shipped and is no longer in the refreshed buffer.
      const refreshed = shippingReducer(withSelection, {
        type: 'BUFFER_REFRESHED',
        result: scanBufferResult({
          products: [products[1]],
          count: 1,
        }),
      })

      expect(refreshed.products).toHaveLength(1)
      expect(refreshed.selectedProductIds).toEqual([])
    })
  })

  describe('navigation', () => {
    it('BACK_TO_DRIVER keeps the buffer but drops the dispatch and mismatch', () => {
      const next = shippingReducer(shipPhaseState({ destination: DEST_7 }), {
        type: 'BACK_TO_DRIVER',
      })

      expect(next.phase).toBe('driver')
      expect(next.bufferBin?.code).toBe('BIN-SHIP-5')
      expect(next.dispatch).toBeNull()
      expect(next.destinationMismatch).toBe(false)
    })

    it('BACK_TO_BUFFER resets to the initial buffer scan', () => {
      const afterBuffer = shippingReducer(initialShippingState, {
        type: 'BUFFER_SCANNED',
        result: scanBufferResult(),
      })
      const next = shippingReducer(afterBuffer, { type: 'BACK_TO_BUFFER' })

      expect(next).toEqual(initialShippingState)
    })

    it('RESET returns to the initial state', () => {
      const done = shippingReducer(shipPhaseState(), {
        type: 'SHIPPED',
        result: shipResult(),
      })
      expect(shippingReducer(done, { type: 'RESET' })).toEqual(
        initialShippingState,
      )
    })
  })

  describe('errors', () => {
    it('SHOW_ERROR sets the message and clears the empty-buffer note', () => {
      const empty = shippingReducer(initialShippingState, {
        type: 'BUFFER_SCANNED',
        result: scanBufferResult({ products: [], count: 0 }),
      })
      const next = shippingReducer(empty, {
        type: 'SHOW_ERROR',
        message: 'Ячейка не найдена',
      })

      expect(next.errorMessage).toBe('Ячейка не найдена')
      expect(next.emptyBufferMessage).toBeNull()
    })

    it('DISMISS_ERROR clears the message', () => {
      const withError = shippingReducer(initialShippingState, {
        type: 'SHOW_ERROR',
        message: 'boom',
      })
      expect(shippingReducer(withError, { type: 'DISMISS_ERROR' }).errorMessage).toBeNull()
    })
  })
})
