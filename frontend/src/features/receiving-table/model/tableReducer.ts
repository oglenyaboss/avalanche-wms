import type {
  CloseCargoplaceSummary,
  ExpectedSku,
  OpenedBox,
  OpenedCargoplace,
  ReceivingProgress,
  RegisteredProduct,
  ResolvedSku,
  ScannedProduct,
} from '@/entities/cargoplace'

// The desk flow walks four phases for one cargoplace:
//   cargoplace → box → product → (back to box | buffer)
// The product phase has two sub-steps: resolve the SKU by barcode, then
// register the physical item by QR.
export type TablePhase = 'cargoplace' | 'box' | 'product' | 'buffer'
export type ProductStep = 'barcode' | 'qr'

export interface ActiveCargoplace {
  cargoplaceId: string
  cargoplaceCode: string
  expectedSkus: ExpectedSku[]
  totalExpected: number
  progress: ReceivingProgress
}

export interface ActiveBox {
  boxId: string
  boxBarcode: string
}

export interface TableState {
  phase: TablePhase
  cargoplace: ActiveCargoplace | null
  box: ActiveBox | null
  productStep: ProductStep
  // Set after scan-sku, cleared after the product is registered by QR.
  resolvedSku: ResolvedSku | null
  // Every product registered for the current cargoplace (running list).
  scannedProducts: ScannedProduct[]
  // Buffer phase: how many products were moved to the buffer bin (null until
  // the buffer is scanned).
  bufferPlaced: number | null
  // Buffer phase: the close summary (null until the cargoplace is closed).
  summary: CloseCargoplaceSummary | null
  errorMessage: string | null
  successMessage: string | null
  confirmCloseBoxOpen: boolean
  confirmAcceptCargoplaceOpen: boolean
}

export type TableAction =
  | { type: 'CARGOPLACE_OPENED'; result: OpenedCargoplace }
  | { type: 'BOX_OPENED'; result: OpenedBox }
  | { type: 'SKU_RESOLVED'; result: ResolvedSku }
  | { type: 'PRODUCT_REGISTERED'; result: RegisteredProduct }
  | { type: 'BOX_CLOSED' }
  | { type: 'GO_TO_BUFFER' }
  | { type: 'BUFFER_SCANNED'; productsPlaced: number }
  | { type: 'CARGOPLACE_CLOSED'; summary: CloseCargoplaceSummary }
  | { type: 'FINISH' }
  | { type: 'OPEN_CONFIRM_CLOSE_BOX' }
  | { type: 'CANCEL_CONFIRM_CLOSE_BOX' }
  | { type: 'OPEN_CONFIRM_ACCEPT_CARGOPLACE' }
  | { type: 'CANCEL_CONFIRM_ACCEPT_CARGOPLACE' }
  | { type: 'SHOW_ERROR'; message: string }
  | { type: 'SHOW_TERMINAL_ERROR'; message: string }
  | { type: 'BOX_LOST'; message: string }
  | { type: 'DISMISS_ERROR' }
  | { type: 'RESET' }

export const initialTableState: TableState = {
  phase: 'cargoplace',
  cargoplace: null,
  box: null,
  productStep: 'barcode',
  resolvedSku: null,
  scannedProducts: [],
  bufferPlaced: null,
  summary: null,
  errorMessage: null,
  successMessage: null,
  confirmCloseBoxOpen: false,
  confirmAcceptCargoplaceOpen: false,
}

function buildFinishMessage(
  code: string,
  summary: CloseCargoplaceSummary,
): string {
  if (summary.shortage > 0) {
    return `Грузоместо ${code} обработано. Принято ${summary.productsReceived} из ${summary.productsExpected}, недостача ${summary.shortage}.`
  }

  return `Грузоместо ${code} обработано. Принято ${summary.productsReceived} из ${summary.productsExpected}.`
}

export function tableReducer(
  state: TableState,
  action: TableAction,
): TableState {
  switch (action.type) {
    case 'CARGOPLACE_OPENED': {
      const { result } = action
      return {
        ...initialTableState,
        phase: 'box',
        cargoplace: {
          cargoplaceId: result.cargoplaceId,
          cargoplaceCode: result.cargoplaceCode,
          expectedSkus: result.expectedSkus,
          totalExpected: result.totalExpected,
          progress: { received: 0, expected: result.totalExpected },
        },
      }
    }

    case 'BOX_OPENED': {
      if (!state.cargoplace) {
        return state
      }

      return {
        ...state,
        phase: 'product',
        productStep: 'barcode',
        resolvedSku: null,
        box: {
          boxId: action.result.boxId,
          boxBarcode: action.result.boxBarcode,
        },
        errorMessage: null,
      }
    }

    case 'SKU_RESOLVED': {
      if (!state.box) {
        return state
      }

      return {
        ...state,
        productStep: 'qr',
        resolvedSku: action.result,
        errorMessage: null,
      }
    }

    case 'PRODUCT_REGISTERED': {
      if (!state.cargoplace) {
        return state
      }

      const { result } = action
      const scannedProduct: ScannedProduct = {
        productId: result.productId,
        skuId: result.skuId,
        skuName: result.skuName,
        qrCode: result.qrCode,
      }

      return {
        ...state,
        productStep: 'barcode',
        resolvedSku: null,
        scannedProducts: [...state.scannedProducts, scannedProduct],
        cargoplace: { ...state.cargoplace, progress: result.progress },
        errorMessage: null,
      }
    }

    case 'BOX_CLOSED': {
      return {
        ...state,
        phase: 'box',
        box: null,
        productStep: 'barcode',
        resolvedSku: null,
        confirmCloseBoxOpen: false,
        errorMessage: null,
      }
    }

    case 'GO_TO_BUFFER': {
      return {
        ...state,
        phase: 'buffer',
        bufferPlaced: null,
        summary: null,
        confirmAcceptCargoplaceOpen: false,
        errorMessage: null,
      }
    }

    case 'BUFFER_SCANNED': {
      return {
        ...state,
        bufferPlaced: action.productsPlaced,
        errorMessage: null,
      }
    }

    case 'CARGOPLACE_CLOSED': {
      return {
        ...state,
        summary: action.summary,
        errorMessage: null,
      }
    }

    case 'FINISH': {
      const code = state.cargoplace?.cargoplaceCode ?? ''
      const successMessage = state.summary
        ? buildFinishMessage(code, state.summary)
        : null

      return { ...initialTableState, successMessage }
    }

    case 'OPEN_CONFIRM_CLOSE_BOX': {
      return { ...state, confirmCloseBoxOpen: true }
    }

    case 'CANCEL_CONFIRM_CLOSE_BOX': {
      return { ...state, confirmCloseBoxOpen: false }
    }

    case 'OPEN_CONFIRM_ACCEPT_CARGOPLACE': {
      return { ...state, confirmAcceptCargoplaceOpen: true }
    }

    case 'CANCEL_CONFIRM_ACCEPT_CARGOPLACE': {
      return { ...state, confirmAcceptCargoplaceOpen: false }
    }

    case 'SHOW_ERROR': {
      return {
        ...state,
        errorMessage: action.message,
        confirmCloseBoxOpen: false,
        confirmAcceptCargoplaceOpen: false,
        successMessage: null,
      }
    }

    // The cargoplace died underneath us (closed / not in progress / not found):
    // surface the message and drop back to the initial cargoplace scan.
    case 'SHOW_TERMINAL_ERROR': {
      return { ...initialTableState, errorMessage: action.message }
    }

    // The current box is gone but the cargoplace is still open: drop back to
    // the box scan, keeping the cargoplace and the products scanned so far.
    case 'BOX_LOST': {
      if (!state.cargoplace) {
        return { ...initialTableState, errorMessage: action.message }
      }

      return {
        ...state,
        phase: 'box',
        box: null,
        productStep: 'barcode',
        resolvedSku: null,
        confirmCloseBoxOpen: false,
        confirmAcceptCargoplaceOpen: false,
        errorMessage: action.message,
      }
    }

    case 'DISMISS_ERROR': {
      return { ...state, errorMessage: null }
    }

    case 'RESET': {
      return initialTableState
    }

    default: {
      // Exhaustiveness guard: if a new TableAction variant is added without a
      // matching case, this assignment fails to compile.
      const _exhaustive: never = action
      void _exhaustive
      return state
    }
  }
}
