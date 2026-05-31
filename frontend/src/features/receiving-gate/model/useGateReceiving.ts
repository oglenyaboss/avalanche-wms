import { useReducer } from 'react'
import { useMutation } from '@tanstack/react-query'

import {
  acceptShipment,
  scanGateCargoplace,
  scanTtn,
} from '../api/receivingGateApi'
import { getReceivingErrorMessage, isTerminalShipmentError } from './errors'
import { gateReducer, initialGateState } from './gateReducer'

export function useGateReceiving() {
  const [state, dispatch] = useReducer(gateReducer, initialGateState)

  const scanTtnMutation = useMutation({
    mutationKey: ['receiving', 'gate', 'scan-ttn'],
    mutationFn: scanTtn,
    retry: false,
  })
  const scanCargoplaceMutation = useMutation({
    mutationKey: ['receiving', 'gate', 'scan-cargoplace'],
    mutationFn: scanGateCargoplace,
    retry: false,
  })
  const acceptShipmentMutation = useMutation({
    mutationKey: ['receiving', 'gate', 'accept-shipment'],
    mutationFn: acceptShipment,
    retry: false,
  })

  const submitTtn = async (code: string) => {
    if (scanTtnMutation.isPending) {
      return
    }

    try {
      const shipment = await scanTtnMutation.mutateAsync(code)
      dispatch({ type: 'TTN_OPENED', shipment })
    } catch (error) {
      dispatch({ type: 'SHOW_ERROR', message: getReceivingErrorMessage(error) })
    }
  }

  const submitCargoplace = async (code: string) => {
    // Guard against scanner re-entrancy: a keyboard-wedge scanner fires the
    // form submit on Enter, bypassing the disabled button.
    if (!state.shipment || scanCargoplaceMutation.isPending) {
      return
    }

    try {
      const result = await scanCargoplaceMutation.mutateAsync({
        shipmentId: state.shipment.shipmentId,
        cargoplaceCode: code,
      })
      dispatch({ type: 'CARGOPLACE_RECEIVED', result })
    } catch (error) {
      const message = getReceivingErrorMessage(error)
      dispatch(
        isTerminalShipmentError(error)
          ? { type: 'SHOW_TERMINAL_ERROR', message }
          : { type: 'SHOW_ERROR', message },
      )
    }
  }

  const confirmClose = async () => {
    if (!state.shipment || acceptShipmentMutation.isPending) {
      return
    }

    try {
      const result = await acceptShipmentMutation.mutateAsync(
        state.shipment.shipmentId,
      )
      dispatch({ type: 'SHIPMENT_CLOSED', result })
    } catch (error) {
      // Only a genuinely dead shipment resets the flow; a transient 5xx/network
      // error keeps the active shipment so the operator can retry.
      const message = getReceivingErrorMessage(error)
      dispatch(
        isTerminalShipmentError(error)
          ? { type: 'SHOW_TERMINAL_ERROR', message }
          : { type: 'SHOW_ERROR', message },
      )
    }
  }

  return {
    state,
    submitTtn,
    submitCargoplace,
    confirmClose,
    openConfirmClose: () => dispatch({ type: 'OPEN_CONFIRM_CLOSE' }),
    cancelConfirmClose: () => dispatch({ type: 'CANCEL_CONFIRM_CLOSE' }),
    dismissError: () => dispatch({ type: 'DISMISS_ERROR' }),
    isScanningTtn: scanTtnMutation.isPending,
    isScanningCargoplace: scanCargoplaceMutation.isPending,
    isClosing: acceptShipmentMutation.isPending,
  }
}
