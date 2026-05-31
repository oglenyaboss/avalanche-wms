import { describe, expect, it } from 'vitest'

import type {
  BufferScan,
  PickedProductResult,
  StoragePlacement,
} from '@/entities/putaway'

import {
  initialPutawayState,
  putawayReducer,
  type PutawayState,
} from './putawayReducer'

const bufferScan: BufferScan = {
  bufferBinId: 'buf-1',
  bufferCode: 'BUFFER-01',
  products: [
    { productId: 'p-1', skuName: 'Кроссовки', qrCode: 'QR-1', status: 'RECEIVED' },
    { productId: 'p-2', skuName: 'Футболка', qrCode: 'QR-2', status: 'RECEIVED' },
  ],
  totalProducts: 2,
}

function picked(overrides: Partial<PickedProductResult> = {}): PickedProductResult {
  return {
    productId: 'p-1',
    skuName: 'Кроссовки',
    qrCode: 'QR-1',
    cartSize: 2,
    ...overrides,
  }
}

function placement(overrides: Partial<StoragePlacement> = {}): StoragePlacement {
  return {
    storageBinId: 'bin-1',
    storageBinCode: 'A-01-12',
    productsPlaced: 1,
    outboxEventsCreated: 1,
    ...overrides,
  }
}

// Walk the happy path to a buffer scanned with two products in the cart.
function pickTwo(): PutawayState {
  const afterBuffer = putawayReducer(initialPutawayState, {
    type: 'BUFFER_SCANNED',
    result: bufferScan,
  })
  const afterFirst = putawayReducer(afterBuffer, {
    type: 'PRODUCT_PICKED',
    result: picked(),
  })
  return putawayReducer(afterFirst, {
    type: 'PRODUCT_PICKED',
    result: picked({ productId: 'p-2', skuName: 'Футболка', qrCode: 'QR-2' }),
  })
}

describe('putawayReducer / BUFFER_SCANNED', () => {
  it('enters the pick phase with the buffer context and an empty cart', () => {
    const state = putawayReducer(initialPutawayState, {
      type: 'BUFFER_SCANNED',
      result: bufferScan,
    })

    expect(state.phase).toBe('pick')
    expect(state.buffer).toEqual({
      bufferBinId: 'buf-1',
      bufferCode: 'BUFFER-01',
      products: bufferScan.products,
      totalProducts: 2,
    })
    expect(state.cart).toEqual([])
  })

  it('restarts the task when a buffer is re-scanned mid-flow', () => {
    const dirty: PutawayState = {
      ...pickTwo(),
      placedProductIds: ['p-1'],
      successMessage: 'old',
    }

    const state = putawayReducer(dirty, {
      type: 'BUFFER_SCANNED',
      result: bufferScan,
    })

    expect(state.cart).toEqual([])
    expect(state.placedProductIds).toEqual([])
    expect(state.successMessage).toBeNull()
  })
})

describe('putawayReducer / PRODUCT_PICKED', () => {
  it('adds the picked product to the cart and records the buffer cart size', () => {
    const afterBuffer = putawayReducer(initialPutawayState, {
      type: 'BUFFER_SCANNED',
      result: bufferScan,
    })

    const state = putawayReducer(afterBuffer, {
      type: 'PRODUCT_PICKED',
      result: picked({ cartSize: 5 }),
    })

    expect(state.cart).toEqual([
      { productId: 'p-1', skuName: 'Кроссовки', qrCode: 'QR-1' },
    ])
    expect(state.cartSize).toBe(5)
  })

  it('does not add the same product to the cart twice', () => {
    const afterBuffer = putawayReducer(initialPutawayState, {
      type: 'BUFFER_SCANNED',
      result: bufferScan,
    })
    const once = putawayReducer(afterBuffer, {
      type: 'PRODUCT_PICKED',
      result: picked(),
    })
    const twice = putawayReducer(once, {
      type: 'PRODUCT_PICKED',
      result: picked(),
    })

    expect(twice.cart).toHaveLength(1)
  })

  it('ignores picks outside the pick phase', () => {
    const state = putawayReducer(initialPutawayState, {
      type: 'PRODUCT_PICKED',
      result: picked(),
    })

    expect(state).toBe(initialPutawayState)
  })
})

describe('putawayReducer / GO_TO_STORAGE', () => {
  it('moves to the storage bin step when the cart is non-empty', () => {
    const state = putawayReducer(pickTwo(), { type: 'GO_TO_STORAGE' })

    expect(state.phase).toBe('storage')
    expect(state.storageStep).toBe('bin')
    expect(state.activeBin).toBeNull()
  })

  it('refuses to advance with an empty cart', () => {
    const afterBuffer = putawayReducer(initialPutawayState, {
      type: 'BUFFER_SCANNED',
      result: bufferScan,
    })

    const state = putawayReducer(afterBuffer, { type: 'GO_TO_STORAGE' })

    expect(state.phase).toBe('pick')
  })
})

describe('putawayReducer / storage placement', () => {
  it('sets the active bin and moves to the place step on a bin scan', () => {
    const inStorage = putawayReducer(pickTwo(), { type: 'GO_TO_STORAGE' })

    const state = putawayReducer(inStorage, {
      type: 'STORAGE_BIN_SCANNED',
      storageBinId: 'bin-1',
    })

    expect(state.storageStep).toBe('place')
    expect(state.activeBin).toEqual({ storageBinId: 'bin-1' })
  })

  it('records a placed product without duplicating it', () => {
    const inPlace = putawayReducer(
      putawayReducer(pickTwo(), { type: 'GO_TO_STORAGE' }),
      { type: 'STORAGE_BIN_SCANNED', storageBinId: 'bin-1' },
    )

    const placedOnce = putawayReducer(inPlace, {
      type: 'PRODUCT_PLACED',
      productId: 'p-1',
      result: placement(),
    })
    const placedTwice = putawayReducer(placedOnce, {
      type: 'PRODUCT_PLACED',
      productId: 'p-1',
      result: placement(),
    })

    expect(placedTwice.placedProductIds).toEqual(['p-1'])
    expect(placedTwice.lastPlacement?.storageBinCode).toBe('A-01-12')
  })

  it('returns to the bin step on CHANGE_BIN, keeping placements', () => {
    const inPlace = putawayReducer(
      putawayReducer(pickTwo(), { type: 'GO_TO_STORAGE' }),
      { type: 'STORAGE_BIN_SCANNED', storageBinId: 'bin-1' },
    )
    const placed = putawayReducer(inPlace, {
      type: 'PRODUCT_PLACED',
      productId: 'p-1',
      result: placement(),
    })

    const state = putawayReducer(placed, { type: 'CHANGE_BIN' })

    expect(state.storageStep).toBe('bin')
    expect(state.activeBin).toBeNull()
    expect(state.placedProductIds).toEqual(['p-1'])
    // The success banner belongs to the old bin and must not linger.
    expect(state.lastPlacement).toBeNull()
  })

  it('clears the previous placement banner when a new bin is scanned', () => {
    const placed = putawayReducer(
      putawayReducer(
        putawayReducer(pickTwo(), { type: 'GO_TO_STORAGE' }),
        { type: 'STORAGE_BIN_SCANNED', storageBinId: 'bin-1' },
      ),
      { type: 'PRODUCT_PLACED', productId: 'p-1', result: placement() },
    )

    const state = putawayReducer(
      putawayReducer(placed, { type: 'CHANGE_BIN' }),
      { type: 'STORAGE_BIN_SCANNED', storageBinId: 'bin-2' },
    )

    expect(state.activeBin).toEqual({ storageBinId: 'bin-2' })
    expect(state.lastPlacement).toBeNull()
  })

  it('ignores a placement dispatched outside the storage phase', () => {
    const inPick = putawayReducer(initialPutawayState, {
      type: 'BUFFER_SCANNED',
      result: bufferScan,
    })

    const state = putawayReducer(inPick, {
      type: 'PRODUCT_PLACED',
      productId: 'p-1',
      result: placement(),
    })

    expect(state).toBe(inPick)
  })
})

describe('putawayReducer / FINISH_TASK', () => {
  it('resets to the buffer phase with a summary success message', () => {
    const placed = putawayReducer(
      putawayReducer(
        putawayReducer(pickTwo(), { type: 'GO_TO_STORAGE' }),
        { type: 'STORAGE_BIN_SCANNED', storageBinId: 'bin-1' },
      ),
      { type: 'PRODUCT_PLACED', productId: 'p-1', result: placement() },
    )

    const state = putawayReducer(placed, { type: 'FINISH_TASK' })

    expect(state.phase).toBe('buffer')
    expect(state.buffer).toBeNull()
    expect(state.successMessage).toContain('Размещено товаров: 1')
    expect(state.successMessage).toContain('BUFFER-01')
  })
})

describe('putawayReducer / error handling', () => {
  it('surfaces an error and clears any open confirm dialog', () => {
    const state = putawayReducer(
      { ...pickTwo(), confirmFinishOpen: true },
      { type: 'SHOW_ERROR', message: 'Товар не в буфере' },
    )

    expect(state.errorMessage).toBe('Товар не в буфере')
    expect(state.confirmFinishOpen).toBe(false)
  })

  it('dismisses the error without losing the cart', () => {
    const withError = putawayReducer(pickTwo(), {
      type: 'SHOW_ERROR',
      message: 'boom',
    })

    const state = putawayReducer(withError, { type: 'DISMISS_ERROR' })

    expect(state.errorMessage).toBeNull()
    expect(state.cart).toHaveLength(2)
  })
})
