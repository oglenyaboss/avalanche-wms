import { describe, expect, it } from 'vitest'

import type {
  CargoplaceScanResult,
  OpenedShipment,
  ShipmentCloseResult,
} from '@/entities/shipment'

import { gateReducer, initialGateState, type GateState } from './gateReducer'

const openedAllExpected: OpenedShipment = {
  shipmentId: 's1',
  ttnCode: 'TTN-1',
  status: 'GATE_IN_PROGRESS',
  cargoplaces: [
    { id: 'c1', code: 'CP-1', status: 'EXPECTED' },
    { id: 'c2', code: 'CP-2', status: 'EXPECTED' },
  ],
  totalCargoplaces: 2,
  receivedCargoplaces: 0,
}

function open(shipment: OpenedShipment = openedAllExpected): GateState {
  return gateReducer(initialGateState, { type: 'TTN_OPENED', shipment })
}

function scanResult(
  overrides: Partial<CargoplaceScanResult> = {},
): CargoplaceScanResult {
  return {
    cargoplace: { id: 'c1', code: 'CP-1', status: 'RECEIVED_AT_GATE' },
    receivedAtGate: '2026-05-31T10:00:00Z',
    progress: { total: 2, received: 1, remaining: 1, notReceived: 0 },
    ...overrides,
  }
}

describe('gateReducer / TTN_OPENED', () => {
  it('enters the shipment phase and derives progress from a fresh shipment', () => {
    const state = open()

    expect(state.phase).toBe('shipment')
    expect(state.shipment?.ttnCode).toBe('TTN-1')
    expect(state.shipment?.progress).toEqual({
      total: 2,
      received: 0,
      remaining: 2,
      notReceived: 0,
    })
  })

  it('does NOT assume all-EXPECTED on a re-scanned, partially-received shipment', () => {
    const state = open({
      ...openedAllExpected,
      cargoplaces: [
        { id: 'c1', code: 'CP-1', status: 'RECEIVED_AT_GATE' },
        { id: 'c2', code: 'CP-2', status: 'EXPECTED' },
      ],
      receivedCargoplaces: 1,
    })

    expect(state.shipment?.progress).toEqual({
      total: 2,
      received: 1,
      remaining: 1,
      notReceived: 0,
    })
  })

  it('clears any prior success/error message', () => {
    const dirty: GateState = {
      ...initialGateState,
      errorMessage: 'old',
      successMessage: 'old',
    }
    const state = gateReducer(dirty, {
      type: 'TTN_OPENED',
      shipment: openedAllExpected,
    })

    expect(state.errorMessage).toBeNull()
    expect(state.successMessage).toBeNull()
  })
})

describe('gateReducer / CARGOPLACE_RECEIVED', () => {
  it('marks the scanned place received and keeps scanning while remaining > 0', () => {
    const state = gateReducer(open(), {
      type: 'CARGOPLACE_RECEIVED',
      result: scanResult(),
    })

    expect(state.phase).toBe('shipment')
    expect(state.shipment?.cargoplaces.find((c) => c.id === 'c1')?.status).toBe(
      'RECEIVED_AT_GATE',
    )
    expect(state.shipment?.cargoplaces.find((c) => c.id === 'c2')?.status).toBe(
      'EXPECTED',
    )
    expect(state.shipment?.progress.received).toBe(1)
  })

  it('auto-closes (resets + success) when remaining reaches 0', () => {
    const state = gateReducer(open(), {
      type: 'CARGOPLACE_RECEIVED',
      result: scanResult({
        cargoplace: { id: 'c2', code: 'CP-2', status: 'RECEIVED_AT_GATE' },
        progress: { total: 2, received: 2, remaining: 0, notReceived: 0 },
      }),
    })

    expect(state.phase).toBe('ttn')
    expect(state.shipment).toBeNull()
    expect(state.successMessage).toContain('принята полностью')
    expect(state.successMessage).toContain('2 из 2')
  })

  it('is a no-op when there is no active shipment', () => {
    const state = gateReducer(initialGateState, {
      type: 'CARGOPLACE_RECEIVED',
      result: scanResult(),
    })

    expect(state).toBe(initialGateState)
  })
})

describe('gateReducer / SHIPMENT_CLOSED', () => {
  it('resets and reports a partial summary', () => {
    const result: ShipmentCloseResult = {
      shipmentId: 's1',
      status: 'GATE_CLOSED',
      summary: { total: 2, received: 1, remaining: 0, notReceived: 1 },
    }
    const state = gateReducer(open(), { type: 'SHIPMENT_CLOSED', result })

    expect(state.phase).toBe('ttn')
    expect(state.shipment).toBeNull()
    expect(state.successMessage).toContain('Принято 1 из 2')
    expect(state.successMessage).toContain('не принято 1')
  })
})

describe('gateReducer / errors & dialogs', () => {
  it('SHOW_ERROR keeps the active shipment (non-terminal)', () => {
    const state = gateReducer(open(), { type: 'SHOW_ERROR', message: 'oops' })

    expect(state.phase).toBe('shipment')
    expect(state.shipment).not.toBeNull()
    expect(state.errorMessage).toBe('oops')
    expect(state.confirmCloseOpen).toBe(false)
  })

  it('SHOW_TERMINAL_ERROR drops back to the TTN scan', () => {
    const opened = gateReducer(open(), { type: 'OPEN_CONFIRM_CLOSE' })
    const state = gateReducer(opened, {
      type: 'SHOW_TERMINAL_ERROR',
      message: 'dead shipment',
    })

    expect(state.phase).toBe('ttn')
    expect(state.shipment).toBeNull()
    expect(state.errorMessage).toBe('dead shipment')
  })

  it('toggles the confirm-close dialog and dismisses errors', () => {
    expect(gateReducer(open(), { type: 'OPEN_CONFIRM_CLOSE' }).confirmCloseOpen).toBe(
      true,
    )

    const withError = gateReducer(open(), {
      type: 'SHOW_ERROR',
      message: 'x',
    })
    expect(gateReducer(withError, { type: 'DISMISS_ERROR' }).errorMessage).toBeNull()
  })
})
