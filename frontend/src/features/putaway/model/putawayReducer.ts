import type {
  BufferScan,
  PickedProduct,
  PickedProductResult,
  StoragePlacement,
} from '@/entities/putaway'

// The putaway flow walks three phases for one buffer:
//   buffer → pick → storage
// The storage phase has one step: scan the target bin — one signed action
// places the whole unplaced cart into it.
export type PutawayPhase = 'buffer' | 'pick' | 'storage'

export interface ActiveBuffer {
  bufferBinId: string
  bufferCode: string
  // RECEIVED products in the buffer at scan time — the qr_code ↔ product_id
  // map the UI uses to resolve a scanned QR to a product_id.
  products: BufferScan['products']
  totalProducts: number
}

export interface PutawayState {
  phase: PutawayPhase
  buffer: ActiveBuffer | null
  // The putaway cart: products picked from the buffer for this task.
  cart: PickedProduct[]
  // Buffer's RECEIVED inventory count from the last scan-product (informational
  // — it does not decrement as items are picked).
  cartSize: number | null
  // product_ids already placed into a storage bin during this task.
  placedProductIds: string[]
  // The last successful placement (drives the running success message).
  lastPlacement: StoragePlacement | null
  // The "Товары на раскладку" drawer.
  drafterOpen: boolean
  errorMessage: string | null
  successMessage: string | null
  confirmFinishOpen: boolean
}

export type PutawayAction =
  | { type: 'BUFFER_SCANNED'; result: BufferScan }
  | { type: 'PRODUCT_PICKED'; result: PickedProductResult }
  | { type: 'GO_TO_STORAGE' }
  | {
      type: 'PRODUCTS_PLACED'
      productIds: string[]
      result: StoragePlacement
    }
  | { type: 'OPEN_DRAFTER' }
  | { type: 'CLOSE_DRAFTER' }
  | { type: 'OPEN_CONFIRM_FINISH' }
  | { type: 'CANCEL_CONFIRM_FINISH' }
  | { type: 'FINISH_TASK' }
  | { type: 'SHOW_ERROR'; message: string }
  | { type: 'DISMISS_ERROR' }
  | { type: 'RESET' }

export const initialPutawayState: PutawayState = {
  phase: 'buffer',
  buffer: null,
  cart: [],
  cartSize: null,
  placedProductIds: [],
  lastPlacement: null,
  drafterOpen: false,
  errorMessage: null,
  successMessage: null,
  confirmFinishOpen: false,
}

function buildFinishMessage(state: PutawayState): string {
  const placed = state.placedProductIds.length
  const code = state.buffer?.bufferCode ?? ''
  return `Задача раскладки из буфера ${code} завершена. Размещено товаров: ${placed}.`
}

export function putawayReducer(
  state: PutawayState,
  action: PutawayAction,
): PutawayState {
  switch (action.type) {
    // Scanning a buffer (re)starts the task: a fresh cart, no placements.
    case 'BUFFER_SCANNED': {
      const { result } = action
      return {
        ...initialPutawayState,
        phase: 'pick',
        buffer: {
          bufferBinId: result.bufferBinId,
          bufferCode: result.bufferCode,
          products: result.products,
          totalProducts: result.totalProducts,
        },
      }
    }

    case 'PRODUCT_PICKED': {
      if (state.phase !== 'pick') {
        return state
      }

      const { result } = action
      // Dedupe: scanning the same product twice keeps one cart entry.
      const alreadyPicked = state.cart.some(
        (item) => item.productId === result.productId,
      )
      const cart = alreadyPicked
        ? state.cart
        : [
            ...state.cart,
            {
              productId: result.productId,
              skuName: result.skuName,
              qrCode: result.qrCode,
            },
          ]

      return {
        ...state,
        cart,
        cartSize: result.cartSize,
        errorMessage: null,
      }
    }

    case 'GO_TO_STORAGE': {
      if (state.phase !== 'pick' || state.cart.length === 0) {
        return state
      }

      return {
        ...state,
        phase: 'storage',
        drafterOpen: false,
        errorMessage: null,
      }
    }

    case 'PRODUCTS_PLACED': {
      if (state.phase !== 'storage') {
        return state
      }

      // One signed action places the whole unplaced cart into the scanned
      // bin; the batch lands as a single state transition.
      const placedProductIds = state.placedProductIds.slice()
      for (const productId of action.productIds) {
        if (!placedProductIds.includes(productId)) {
          placedProductIds.push(productId)
        }
      }

      return {
        ...state,
        placedProductIds,
        lastPlacement: action.result,
        errorMessage: null,
      }
    }

    case 'OPEN_DRAFTER': {
      return { ...state, drafterOpen: true }
    }

    case 'CLOSE_DRAFTER': {
      return { ...state, drafterOpen: false }
    }

    case 'OPEN_CONFIRM_FINISH': {
      return { ...state, confirmFinishOpen: true }
    }

    case 'CANCEL_CONFIRM_FINISH': {
      return { ...state, confirmFinishOpen: false }
    }

    case 'FINISH_TASK': {
      return { ...initialPutawayState, successMessage: buildFinishMessage(state) }
    }

    case 'SHOW_ERROR': {
      return {
        ...state,
        errorMessage: action.message,
        confirmFinishOpen: false,
        successMessage: null,
      }
    }

    case 'DISMISS_ERROR': {
      return { ...state, errorMessage: null }
    }

    case 'RESET': {
      return initialPutawayState
    }

    default: {
      // Exhaustiveness guard: a new PutawayAction variant without a matching
      // case fails to compile here.
      const _exhaustive: never = action
      void _exhaustive
      return state
    }
  }
}
