import type {
  AllocationResult,
  AssemblyTask,
  Destination,
  PickedProduct,
  PickResult,
  ShippingBufferResult,
} from '@/entities/assembly'

// The assembly flow walks three phases for one destination:
//   destination → pick → buffer
// destination: choose a store, allocate its orders, load pick tasks.
// pick: scan each product's QR to take it into the operator's cart.
// buffer: scan the shipping-buffer bin once to move the whole cart into it.
export type AssemblyPhase = 'destination' | 'pick' | 'buffer'

export interface AssemblyState {
  phase: AssemblyPhase
  // The store list, loaded once on mount and kept across task resets.
  destinations: Destination[]
  selectedDestination: Destination | null
  // The last allocate result (drives the allocation summary alerts).
  allocation: AllocationResult | null
  // PENDING pick tasks for the selected destination — the qr_code ↔ product_id
  // map the UI uses to resolve a scanned QR to a product_id.
  tasks: AssemblyTask[]
  // The assembly cart: products picked from storage for this destination.
  cart: PickedProduct[]
  // The operator's running cart count from the last pick (server-side).
  cartSize: number | null
  // The last shipping-buffer placement (drives the finish success message).
  lastBufferResult: ShippingBufferResult | null
  // The "Товары к сборке" drawer.
  drafterOpen: boolean
  errorMessage: string | null
  successMessage: string | null
  confirmFinishOpen: boolean
}

export type AssemblyAction =
  | { type: 'DESTINATIONS_LOADED'; destinations: Destination[] }
  | { type: 'DESTINATION_SELECTED'; destination: Destination }
  | { type: 'DESELECT_DESTINATION' }
  // destinationId stamps which store the async result is for, so a late
  // response from a previously-selected store cannot overwrite the current one.
  | { type: 'ALLOCATED'; destinationId: string; result: AllocationResult }
  | { type: 'TASKS_LOADED'; destinationId: string; tasks: AssemblyTask[] }
  | { type: 'START_PICKING' }
  | { type: 'PRODUCT_PICKED'; result: PickResult; task: AssemblyTask }
  | { type: 'GO_TO_BUFFER' }
  | { type: 'BACK_TO_PICK' }
  | { type: 'BUFFER_SCANNED'; result: ShippingBufferResult }
  | { type: 'OPEN_DRAFTER' }
  | { type: 'CLOSE_DRAFTER' }
  | { type: 'OPEN_CONFIRM_FINISH' }
  | { type: 'CANCEL_CONFIRM_FINISH' }
  | { type: 'FINISH_TASK' }
  | { type: 'SHOW_ERROR'; message: string }
  | { type: 'DISMISS_ERROR' }
  | { type: 'RESET' }

export const initialAssemblyState: AssemblyState = {
  phase: 'destination',
  destinations: [],
  selectedDestination: null,
  allocation: null,
  tasks: [],
  cart: [],
  cartSize: null,
  lastBufferResult: null,
  drafterOpen: false,
  errorMessage: null,
  successMessage: null,
  confirmFinishOpen: false,
}

// Reset to the resting (destination) phase for the next store, keeping the
// loaded destination list and surfacing a success summary.
function restWith(
  destinations: Destination[],
  successMessage: string,
): AssemblyState {
  return { ...initialAssemblyState, destinations, successMessage }
}

function buildBufferMessage(result: ShippingBufferResult): string {
  return `Размещено товаров: ${result.productsPlaced}. Заказов собрано: ${result.ordersAssembled}.`
}

export function assemblyReducer(
  state: AssemblyState,
  action: AssemblyAction,
): AssemblyState {
  switch (action.type) {
    case 'DESTINATIONS_LOADED': {
      return { ...state, destinations: action.destinations }
    }

    // Selecting a store starts a fresh task: clear any prior allocation,
    // tasks and cart, but keep the loaded destination list and clear a stale
    // success message from a previous task.
    case 'DESTINATION_SELECTED': {
      if (state.phase !== 'destination') {
        return state
      }

      return {
        ...initialAssemblyState,
        destinations: state.destinations,
        selectedDestination: action.destination,
      }
    }

    case 'DESELECT_DESTINATION': {
      if (state.phase !== 'destination') {
        return state
      }

      return {
        ...initialAssemblyState,
        destinations: state.destinations,
      }
    }

    case 'ALLOCATED': {
      // Drop a late response for a store the operator already navigated away
      // from (guards against the select-A → deselect → select-B race).
      if (
        state.phase !== 'destination' ||
        state.selectedDestination?.destinationId !== action.destinationId
      ) {
        return state
      }

      return { ...state, allocation: action.result, errorMessage: null }
    }

    case 'TASKS_LOADED': {
      if (
        state.phase !== 'destination' ||
        state.selectedDestination?.destinationId !== action.destinationId
      ) {
        return state
      }

      return { ...state, tasks: action.tasks, errorMessage: null }
    }

    case 'START_PICKING': {
      if (
        state.phase !== 'destination' ||
        !state.selectedDestination ||
        state.tasks.length === 0
      ) {
        return state
      }

      return {
        ...state,
        phase: 'pick',
        cart: [],
        cartSize: null,
        drafterOpen: false,
        errorMessage: null,
        successMessage: null,
      }
    }

    case 'PRODUCT_PICKED': {
      if (state.phase !== 'pick') {
        return state
      }

      const { result, task } = action
      // Dedupe: scanning the same product twice keeps one cart entry. The cart
      // entry is built from the resolved task — the pick response only carries
      // product_id and cart_size.
      const alreadyPicked = state.cart.some(
        (item) => item.productId === result.productId,
      )
      const cart = alreadyPicked
        ? state.cart
        : [
            ...state.cart,
            {
              productId: task.productId,
              skuName: task.skuName,
              qrCode: task.qrCode,
              fromBinCode: task.fromBinCode,
              orderNo: task.orderNo,
            },
          ]

      return {
        ...state,
        cart,
        cartSize: result.cartSize,
        errorMessage: null,
      }
    }

    case 'GO_TO_BUFFER': {
      if (state.phase !== 'pick' || state.cart.length === 0) {
        return state
      }

      return {
        ...state,
        phase: 'buffer',
        drafterOpen: false,
        errorMessage: null,
      }
    }

    case 'BACK_TO_PICK': {
      if (state.phase !== 'buffer') {
        return state
      }

      return { ...state, phase: 'pick', errorMessage: null }
    }

    // Scanning the shipping buffer is TERMINAL: it moves the whole cart in one
    // call, then the task is done. Reset to the resting (destination) phase
    // with a success summary so the operator can start the next store.
    case 'BUFFER_SCANNED': {
      if (state.phase !== 'buffer') {
        return state
      }

      return restWith(state.destinations, buildBufferMessage(action.result))
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

    // Interrupting the task abandons the current pick batch WITHOUT moving it to
    // a buffer. Reset to the store picker with NO success message — the products
    // are still ASSEMBLED server-side, so claiming the task is "done" would be a
    // lie. Completion only happens via BUFFER_SCANNED.
    case 'FINISH_TASK': {
      return { ...initialAssemblyState, destinations: state.destinations }
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
      // Keep the loaded destination list; only the task progress is dropped.
      return { ...initialAssemblyState, destinations: state.destinations }
    }

    default: {
      // Exhaustiveness guard: a new AssemblyAction variant without a matching
      // case fails to compile here.
      const _exhaustive: never = action
      void _exhaustive
      return state
    }
  }
}
