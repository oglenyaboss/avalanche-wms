import type {
  DispatchInfo,
  ScanBufferResult,
  ShipResult,
  ShippingBufferBin,
  ShippingBufferProduct,
} from '@/entities/shipping'

// The shipping flow walks four phases for one buffer + truck:
//   buffer → driver → ship → done
// buffer: scan the SHIPPING_BUFFER bin; see the store and its READY_TO_SHIP items.
// driver: scan the dispatch code (driver QR); the truck arrives at the gate.
// ship: ship the whole buffer (bulk) or the scanned/selected items (spot).
// done: the buffer emptied and the truck departed — a terminal summary.
export type ShippingPhase = 'buffer' | 'driver' | 'ship' | 'done'

export interface ShippingState {
  phase: ShippingPhase
  // The scanned buffer bin and its current READY_TO_SHIP contents.
  bufferBin: ShippingBufferBin | null
  products: ShippingBufferProduct[]
  // The dispatch (рейс) registered at the gate.
  dispatch: DispatchInfo | null
  // Spot-mode selection: product IDs to ship in this round. Always deduped and a
  // subset of products[]. Empty → the operator ships in bulk instead.
  selectedProductIds: string[]
  // The last ship result (drives the done-phase summary).
  lastShipResult: ShipResult | null
  // true when bufferBin.destination.id !== dispatch.destination.id — the truck is
  // assigned to a different zone. Blocks shipping (the backend rejects it too).
  destinationMismatch: boolean
  // Info note when a scanned buffer holds nothing ready to ship (stays in buffer
  // phase so the operator can scan another).
  emptyBufferMessage: string | null
  // Inline note after a partial ship: some shipped, more remain in the buffer.
  partialMessage: string | null
  errorMessage: string | null
}

export type ShippingAction =
  | { type: 'BUFFER_SCANNED'; result: ScanBufferResult }
  | { type: 'DRIVER_SCANNED'; result: DispatchInfo }
  | { type: 'TOGGLE_PRODUCT'; productId: string }
  | { type: 'SELECT_PRODUCT'; productId: string }
  | { type: 'CLEAR_SELECTION' }
  | { type: 'SHIPPED'; result: ShipResult }
  | { type: 'BUFFER_REFRESHED'; result: ScanBufferResult }
  | { type: 'BACK_TO_BUFFER' }
  | { type: 'BACK_TO_DRIVER' }
  | { type: 'SHOW_ERROR'; message: string }
  | { type: 'DISMISS_ERROR' }
  | { type: 'RESET' }

export const initialShippingState: ShippingState = {
  phase: 'buffer',
  bufferBin: null,
  products: [],
  dispatch: null,
  selectedProductIds: [],
  lastShipResult: null,
  destinationMismatch: false,
  emptyBufferMessage: null,
  partialMessage: null,
  errorMessage: null,
}

function buildPartialMessage(result: ShipResult): string {
  return `Отгружено товаров: ${result.productsShipped}. Осталось в буфере: ${result.bufferRemaining}.`
}

// Keep only IDs that point at a product currently in the buffer, so a stale
// selection can never leak into a ship request (the backend rejects an exact
// length mismatch with PRODUCT_NOT_IN_BUFFER).
function pruneSelection(
  selected: string[],
  products: ShippingBufferProduct[],
): string[] {
  const present = new Set(products.map((p) => p.productId))
  return selected.filter((id) => present.has(id))
}

export function shippingReducer(
  state: ShippingState,
  action: ShippingAction,
): ShippingState {
  switch (action.type) {
    // Scanning the buffer is the entry point. An empty buffer is not an error:
    // stay on the buffer phase with an info note so the operator can scan again.
    case 'BUFFER_SCANNED': {
      if (state.phase !== 'buffer') {
        return state
      }

      if (action.result.count === 0) {
        return {
          ...state,
          emptyBufferMessage: `Буфер «${action.result.bufferBin.destination.name}» пуст — нет товаров, готовых к отгрузке.`,
          errorMessage: null,
        }
      }

      return {
        ...initialShippingState,
        phase: 'driver',
        bufferBin: action.result.bufferBin,
        products: action.result.products,
      }
    }

    case 'DRIVER_SCANNED': {
      if (state.phase !== 'driver' || !state.bufferBin) {
        return state
      }

      return {
        ...state,
        dispatch: action.result,
        destinationMismatch:
          state.bufferBin.destination.id !== action.result.destination.id,
        selectedProductIds: [],
        phase: 'ship',
        errorMessage: null,
      }
    }

    // Click a product row to flip its spot selection.
    case 'TOGGLE_PRODUCT': {
      if (state.phase !== 'ship') {
        return state
      }
      const inBuffer = state.products.some(
        (p) => p.productId === action.productId,
      )
      if (!inBuffer) {
        return state
      }

      const selected = state.selectedProductIds.includes(action.productId)
        ? state.selectedProductIds.filter((id) => id !== action.productId)
        : [...state.selectedProductIds, action.productId]

      return { ...state, selectedProductIds: selected }
    }

    // Scanning a product QR adds it to the selection (idempotent — re-scanning
    // the same item does not toggle it off, which would surprise the operator).
    case 'SELECT_PRODUCT': {
      if (state.phase !== 'ship') {
        return state
      }
      const inBuffer = state.products.some(
        (p) => p.productId === action.productId,
      )
      if (!inBuffer || state.selectedProductIds.includes(action.productId)) {
        return state
      }

      return {
        ...state,
        selectedProductIds: [...state.selectedProductIds, action.productId],
      }
    }

    case 'CLEAR_SELECTION': {
      return { ...state, selectedProductIds: [] }
    }

    // A ship round completed. If the buffer emptied the dispatch departed →
    // terminal done phase. Otherwise it was a partial ship: stay in the ship
    // phase, surface the partial note, and clear the selection (the hook reloads
    // the remaining buffer right after via BUFFER_REFRESHED).
    case 'SHIPPED': {
      if (state.phase !== 'ship') {
        return state
      }

      if (action.result.dispatchDeparted) {
        return {
          ...state,
          phase: 'done',
          lastShipResult: action.result,
          selectedProductIds: [],
          partialMessage: null,
          errorMessage: null,
        }
      }

      return {
        ...state,
        lastShipResult: action.result,
        selectedProductIds: [],
        partialMessage: buildPartialMessage(action.result),
        errorMessage: null,
      }
    }

    // Reload the buffer after a partial ship so the product list and any kept
    // selection reflect what is actually still ready to ship.
    case 'BUFFER_REFRESHED': {
      if (state.phase !== 'ship') {
        return state
      }

      return {
        ...state,
        bufferBin: action.result.bufferBin,
        products: action.result.products,
        selectedProductIds: pruneSelection(
          state.selectedProductIds,
          action.result.products,
        ),
      }
    }

    // Back to the buffer scan — a fresh start for a different buffer.
    case 'BACK_TO_BUFFER': {
      if (state.phase !== 'driver') {
        return state
      }
      return { ...initialShippingState }
    }

    // Back to the driver scan — used to turn away a mismatched truck and scan the
    // correct one. Keeps the scanned buffer.
    case 'BACK_TO_DRIVER': {
      if (state.phase !== 'ship') {
        return state
      }
      return {
        ...state,
        phase: 'driver',
        dispatch: null,
        destinationMismatch: false,
        selectedProductIds: [],
        partialMessage: null,
        errorMessage: null,
      }
    }

    case 'SHOW_ERROR': {
      return {
        ...state,
        errorMessage: action.message,
        emptyBufferMessage: null,
      }
    }

    case 'DISMISS_ERROR': {
      return { ...state, errorMessage: null }
    }

    case 'RESET': {
      return { ...initialShippingState }
    }

    default: {
      // Exhaustiveness guard: a new ShippingAction variant without a matching
      // case fails to compile here.
      const _exhaustive: never = action
      void _exhaustive
      return state
    }
  }
}
