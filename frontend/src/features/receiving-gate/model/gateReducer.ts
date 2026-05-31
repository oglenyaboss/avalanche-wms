import type {
  Cargoplace,
  CargoplaceScanResult,
  GateProgress,
  OpenedShipment,
  ShipmentCloseResult,
} from '@/entities/shipment'

export type GatePhase = 'ttn' | 'shipment'

export interface ActiveShipment {
  shipmentId: string
  ttnCode: string
  cargoplaces: Cargoplace[]
  progress: GateProgress
}

export interface GateState {
  phase: GatePhase
  shipment: ActiveShipment | null
  errorMessage: string | null
  confirmCloseOpen: boolean
  successMessage: string | null
}

export type GateAction =
  | { type: 'TTN_OPENED'; shipment: OpenedShipment }
  | { type: 'CARGOPLACE_RECEIVED'; result: CargoplaceScanResult }
  | { type: 'SHIPMENT_CLOSED'; result: ShipmentCloseResult }
  | { type: 'SHOW_ERROR'; message: string }
  | { type: 'SHOW_TERMINAL_ERROR'; message: string }
  | { type: 'DISMISS_ERROR' }
  | { type: 'OPEN_CONFIRM_CLOSE' }
  | { type: 'CANCEL_CONFIRM_CLOSE' }
  | { type: 'RESET' }
  | { type: 'CLEAR_SUCCESS' }

export const initialGateState: GateState = {
  phase: 'ttn',
  shipment: null,
  errorMessage: null,
  confirmCloseOpen: false,
  successMessage: null,
}

function progressFromOpenedShipment(shipment: OpenedShipment): GateProgress {
  return {
    total: shipment.totalCargoplaces,
    received: shipment.receivedCargoplaces,
    remaining: shipment.cargoplaces.filter((c) => c.status === 'EXPECTED').length,
    notReceived: shipment.cargoplaces.filter((c) => c.status === 'NOT_RECEIVED')
      .length,
  }
}

function buildAutoCloseMessage(ttnCode: string, progress: GateProgress): string {
  return `Поставка ${ttnCode} принята полностью: ${progress.received} из ${progress.total} грузомест.`
}

function buildCloseMessage(ttnCode: string, summary: GateProgress): string {
  if (summary.notReceived > 0) {
    return `Поставка ${ttnCode} закрыта. Принято ${summary.received} из ${summary.total}, не принято ${summary.notReceived}.`
  }

  return `Поставка ${ttnCode} закрыта. Принято ${summary.received} из ${summary.total}.`
}

export function gateReducer(state: GateState, action: GateAction): GateState {
  switch (action.type) {
    case 'TTN_OPENED': {
      return {
        phase: 'shipment',
        shipment: {
          shipmentId: action.shipment.shipmentId,
          ttnCode: action.shipment.ttnCode,
          cargoplaces: action.shipment.cargoplaces,
          progress: progressFromOpenedShipment(action.shipment),
        },
        errorMessage: null,
        confirmCloseOpen: false,
        successMessage: null,
      }
    }

    case 'CARGOPLACE_RECEIVED': {
      if (!state.shipment) {
        return state
      }

      const { cargoplace, progress } = action.result

      // The scanned place crossed EXPECTED -> RECEIVED_AT_GATE; reflect it.
      const known = state.shipment.cargoplaces.some((c) => c.id === cargoplace.id)
      const cargoplaces = known
        ? state.shipment.cargoplaces.map((c) =>
            c.id === cargoplace.id ? { ...c, status: cargoplace.status } : c,
          )
        : [...state.shipment.cargoplaces, cargoplace]

      // remaining === 0 is the only signal that the shipment auto-closed
      // (the scan-cargoplace response carries no shipment status).
      if (progress.remaining === 0) {
        return {
          ...initialGateState,
          successMessage: buildAutoCloseMessage(state.shipment.ttnCode, progress),
        }
      }

      return {
        ...state,
        shipment: { ...state.shipment, cargoplaces, progress },
        errorMessage: null,
      }
    }

    case 'SHIPMENT_CLOSED': {
      const ttnCode = state.shipment?.ttnCode ?? ''
      return {
        ...initialGateState,
        successMessage: buildCloseMessage(ttnCode, action.result.summary),
      }
    }

    case 'SHOW_ERROR': {
      return {
        ...state,
        errorMessage: action.message,
        confirmCloseOpen: false,
        successMessage: null,
      }
    }

    // Shipment died underneath us (closed / not in progress): surface the
    // message and drop back to the TTN scan so we don't keep a dead shipment.
    case 'SHOW_TERMINAL_ERROR': {
      return { ...initialGateState, errorMessage: action.message }
    }

    case 'DISMISS_ERROR': {
      return { ...state, errorMessage: null }
    }

    case 'OPEN_CONFIRM_CLOSE': {
      return { ...state, confirmCloseOpen: true }
    }

    case 'CANCEL_CONFIRM_CLOSE': {
      return { ...state, confirmCloseOpen: false }
    }

    case 'CLEAR_SUCCESS': {
      return { ...state, successMessage: null }
    }

    case 'RESET': {
      return initialGateState
    }

    default: {
      return state
    }
  }
}
