import { describe, expect, it } from 'vitest'

import type {
  AllocationResult,
  AssemblyTask,
  Destination,
  PickResult,
  ShippingBufferResult,
} from '@/entities/assembly'

import {
  assemblyReducer,
  initialAssemblyState,
  type AssemblyState,
} from './assemblyReducer'

const destinations: Destination[] = [
  {
    destinationId: 'dst-1',
    code: 'STORE-01',
    name: 'Магазин на Тверской',
    address: 'ул. Тверская, 1',
    warehouseId: 1,
  },
  {
    destinationId: 'dst-2',
    code: 'STORE-02',
    name: 'Магазин на Арбате',
    address: null,
    warehouseId: 1,
  },
]

const tasks: AssemblyTask[] = [
  {
    taskId: 't-1',
    productId: 'p-1',
    qrCode: 'QR-1',
    skuName: 'Кроссовки',
    fromBinCode: 'M2-A-03',
    fromBinSection: 'A',
    orderNo: 'ORD-100',
  },
  {
    taskId: 't-2',
    productId: 'p-2',
    qrCode: 'QR-2',
    skuName: 'Футболка',
    fromBinCode: 'M2-B-07',
    fromBinSection: 'B',
    orderNo: 'ORD-101',
  },
]

const allocation: AllocationResult = {
  allocatedOrders: 2,
  allocatedProducts: 2,
  insufficientOrders: [],
}

function picked(overrides: Partial<PickResult> = {}): PickResult {
  return { productId: 'p-1', cartSize: 1, ...overrides }
}

function bufferResult(
  overrides: Partial<ShippingBufferResult> = {},
): ShippingBufferResult {
  return {
    bufferBinId: 'buf-1',
    productsPlaced: 2,
    ordersAssembled: 2,
    ...overrides,
  }
}

// Walk: destinations loaded → store selected → allocate → tasks loaded.
function withTasksLoaded(): AssemblyState {
  const loaded = assemblyReducer(initialAssemblyState, {
    type: 'DESTINATIONS_LOADED',
    destinations,
  })
  const selected = assemblyReducer(loaded, {
    type: 'DESTINATION_SELECTED',
    destination: destinations[0],
  })
  const allocated = assemblyReducer(selected, {
    type: 'ALLOCATED',
    destinationId: destinations[0].destinationId,
    result: allocation,
  })
  return assemblyReducer(allocated, {
    type: 'TASKS_LOADED',
    destinationId: destinations[0].destinationId,
    tasks,
  })
}

// Walk further into the pick phase with two products in the cart.
function pickTwo(): AssemblyState {
  const inPick = assemblyReducer(withTasksLoaded(), { type: 'START_PICKING' })
  const afterFirst = assemblyReducer(inPick, {
    type: 'PRODUCT_PICKED',
    result: picked(),
    task: tasks[0],
  })
  return assemblyReducer(afterFirst, {
    type: 'PRODUCT_PICKED',
    result: picked({ productId: 'p-2', cartSize: 2 }),
    task: tasks[1],
  })
}

describe('assemblyReducer / destination phase', () => {
  it('stores the loaded destination list', () => {
    const state = assemblyReducer(initialAssemblyState, {
      type: 'DESTINATIONS_LOADED',
      destinations,
    })

    expect(state.destinations).toEqual(destinations)
    expect(state.phase).toBe('destination')
  })

  it('selects a store and clears prior task progress', () => {
    const loaded = assemblyReducer(initialAssemblyState, {
      type: 'DESTINATIONS_LOADED',
      destinations,
    })

    const state = assemblyReducer(loaded, {
      type: 'DESTINATION_SELECTED',
      destination: destinations[0],
    })

    expect(state.selectedDestination).toEqual(destinations[0])
    expect(state.tasks).toEqual([])
    expect(state.cart).toEqual([])
    // The store list must survive the reset.
    expect(state.destinations).toEqual(destinations)
  })

  it('deselects back to no store while keeping the list', () => {
    const state = assemblyReducer(withTasksLoaded(), {
      type: 'DESELECT_DESTINATION',
    })

    expect(state.selectedDestination).toBeNull()
    expect(state.tasks).toEqual([])
    expect(state.destinations).toEqual(destinations)
  })

  it('records the allocation result and loaded tasks', () => {
    const state = withTasksLoaded()

    expect(state.allocation).toEqual(allocation)
    expect(state.tasks).toEqual(tasks)
  })

  it('drops a late tasks response for a store the operator left (race guard)', () => {
    const onStore1 = assemblyReducer(
      assemblyReducer(initialAssemblyState, {
        type: 'DESTINATIONS_LOADED',
        destinations,
      }),
      { type: 'DESTINATION_SELECTED', destination: destinations[0] },
    )

    // A late response stamped for dst-2 arrives while dst-1 is selected.
    const state = assemblyReducer(onStore1, {
      type: 'TASKS_LOADED',
      destinationId: destinations[1].destinationId,
      tasks,
    })

    expect(state.tasks).toEqual([])
    expect(state.selectedDestination).toEqual(destinations[0])
  })
})

describe('assemblyReducer / START_PICKING', () => {
  it('enters the pick phase when tasks are present', () => {
    const state = assemblyReducer(withTasksLoaded(), { type: 'START_PICKING' })

    expect(state.phase).toBe('pick')
    expect(state.cart).toEqual([])
  })

  it('refuses to start with no tasks', () => {
    const selected = assemblyReducer(
      assemblyReducer(initialAssemblyState, {
        type: 'DESTINATIONS_LOADED',
        destinations,
      }),
      { type: 'DESTINATION_SELECTED', destination: destinations[0] },
    )

    const state = assemblyReducer(selected, { type: 'START_PICKING' })

    expect(state.phase).toBe('destination')
  })
})

describe('assemblyReducer / PRODUCT_PICKED', () => {
  it('adds the picked product built from its task and records cart size', () => {
    const inPick = assemblyReducer(withTasksLoaded(), { type: 'START_PICKING' })

    const state = assemblyReducer(inPick, {
      type: 'PRODUCT_PICKED',
      result: picked({ cartSize: 5 }),
      task: tasks[0],
    })

    expect(state.cart).toEqual([
      {
        productId: 'p-1',
        skuName: 'Кроссовки',
        qrCode: 'QR-1',
        fromBinCode: 'M2-A-03',
        orderNo: 'ORD-100',
      },
    ])
    expect(state.cartSize).toBe(5)
  })

  it('does not add the same product to the cart twice', () => {
    const inPick = assemblyReducer(withTasksLoaded(), { type: 'START_PICKING' })
    const once = assemblyReducer(inPick, {
      type: 'PRODUCT_PICKED',
      result: picked(),
      task: tasks[0],
    })
    const twice = assemblyReducer(once, {
      type: 'PRODUCT_PICKED',
      result: picked(),
      task: tasks[0],
    })

    expect(twice.cart).toHaveLength(1)
  })

  it('ignores picks outside the pick phase', () => {
    const state = assemblyReducer(withTasksLoaded(), {
      type: 'PRODUCT_PICKED',
      result: picked(),
      task: tasks[0],
    })

    expect(state.cart).toEqual([])
  })
})

describe('assemblyReducer / buffer transitions', () => {
  it('moves to the buffer phase when the cart is non-empty', () => {
    const state = assemblyReducer(pickTwo(), { type: 'GO_TO_BUFFER' })

    expect(state.phase).toBe('buffer')
  })

  it('refuses to advance to the buffer with an empty cart', () => {
    const inPick = assemblyReducer(withTasksLoaded(), { type: 'START_PICKING' })

    const state = assemblyReducer(inPick, { type: 'GO_TO_BUFFER' })

    expect(state.phase).toBe('pick')
  })

  it('returns from buffer back to the pick phase', () => {
    const inBuffer = assemblyReducer(pickTwo(), { type: 'GO_TO_BUFFER' })

    const state = assemblyReducer(inBuffer, { type: 'BACK_TO_PICK' })

    expect(state.phase).toBe('pick')
    expect(state.cart).toHaveLength(2)
  })
})

describe('assemblyReducer / BUFFER_SCANNED (multi-trip)', () => {
  it('places the batch but keeps the store selected so assembly can continue', () => {
    const inBuffer = assemblyReducer(pickTwo(), { type: 'GO_TO_BUFFER' })

    const state = assemblyReducer(inBuffer, {
      type: 'BUFFER_SCANNED',
      result: bufferResult({ productsPlaced: 2, ordersAssembled: 0 }),
    })

    // Back on the store panel with the SAME store still selected — no
    // re-selection needed to keep picking (assembly-flow.md, частичная сдача).
    expect(state.phase).toBe('destination')
    expect(state.selectedDestination).toEqual(destinations[0])
    // The placed batch is cleared; the allocation summary is dropped as stale.
    expect(state.cart).toEqual([])
    expect(state.allocation).toBeNull()
    expect(state.tasks).toEqual([])
    expect(state.destinations).toEqual(destinations)
    expect(state.successMessage).toContain('Размещено товаров: 2')
  })

  it('lets the operator pick the remaining tasks for the same store without re-selecting (independent cycle)', () => {
    // Trip 1: pick two, place them in the buffer.
    const afterTrip1 = assemblyReducer(
      assemblyReducer(pickTwo(), { type: 'GO_TO_BUFFER' }),
      { type: 'BUFFER_SCANNED', result: bufferResult() },
    )

    // The hook reloads the remaining PENDING tasks for the SAME store. The
    // TASKS_LOADED guard (phase==='destination' + matching destinationId) must
    // still accept this — it would silently drop the tasks if BUFFER_SCANNED had
    // deselected the store.
    const remaining: AssemblyTask[] = [tasks[1]]
    const reloaded = assemblyReducer(afterTrip1, {
      type: 'TASKS_LOADED',
      destinationId: destinations[0].destinationId,
      tasks: remaining,
    })
    expect(reloaded.tasks).toEqual(remaining)

    // Trip 2: start picking again — same store continues, fresh empty cart.
    const inPickAgain = assemblyReducer(reloaded, { type: 'START_PICKING' })
    expect(inPickAgain.phase).toBe('pick')
    expect(inPickAgain.cart).toEqual([])
  })

  it('ignores a buffer scan dispatched outside the buffer phase', () => {
    const state = assemblyReducer(pickTwo(), {
      type: 'BUFFER_SCANNED',
      result: bufferResult(),
    })

    expect(state.phase).toBe('pick')
  })
})

describe('assemblyReducer / FINISH_TASK (interrupt)', () => {
  it('abandons the batch and rests on the destination phase WITHOUT a false success message', () => {
    const state = assemblyReducer(pickTwo(), { type: 'FINISH_TASK' })

    expect(state.phase).toBe('destination')
    expect(state.selectedDestination).toBeNull()
    expect(state.cart).toEqual([])
    // Interrupting is not completing — no success summary may be shown.
    expect(state.successMessage).toBeNull()
    expect(state.destinations).toEqual(destinations)
  })
})

describe('assemblyReducer / error handling', () => {
  it('surfaces an error and clears any open confirm dialog', () => {
    const state = assemblyReducer(
      { ...pickTwo(), confirmFinishOpen: true },
      { type: 'SHOW_ERROR', message: 'Корзина пуста' },
    )

    expect(state.errorMessage).toBe('Корзина пуста')
    expect(state.confirmFinishOpen).toBe(false)
  })

  it('dismisses the error without losing the cart', () => {
    const withError = assemblyReducer(pickTwo(), {
      type: 'SHOW_ERROR',
      message: 'boom',
    })

    const state = assemblyReducer(withError, { type: 'DISMISS_ERROR' })

    expect(state.errorMessage).toBeNull()
    expect(state.cart).toHaveLength(2)
  })
})
