import { useReducer } from 'react'
import { useMutation } from '@tanstack/react-query'

import {
  closeBox,
  closeCargoplace,
  scanBox,
  scanBuffer,
  scanQr,
  scanSku,
  scanTableCargoplace,
} from '../api/receivingTableApi'
import {
  getReceivingErrorMessage,
  isBoxTerminalError,
  isCargoplaceTerminalError,
} from './errors'
import {
  initialTableState,
  tableReducer,
  type TableAction,
} from './tableReducer'

// Map any failed request onto the right reducer action: a dead cargoplace
// resets the whole flow, a dead box drops back to the box scan, anything else
// (validation, duplicate QR, transient 5xx) keeps the operator in place.
function errorAction(error: unknown): TableAction {
  const message = getReceivingErrorMessage(error)

  if (isCargoplaceTerminalError(error)) {
    return { type: 'SHOW_TERMINAL_ERROR', message }
  }
  if (isBoxTerminalError(error)) {
    return { type: 'BOX_LOST', message }
  }

  return { type: 'SHOW_ERROR', message }
}

export function useTableReceiving() {
  const [state, dispatch] = useReducer(tableReducer, initialTableState)

  const scanCargoplaceMutation = useMutation({
    mutationKey: ['receiving', 'table', 'scan-cargoplace'],
    mutationFn: scanTableCargoplace,
    retry: false,
  })
  const scanBoxMutation = useMutation({
    mutationKey: ['receiving', 'table', 'scan-box'],
    mutationFn: scanBox,
    retry: false,
  })
  const scanSkuMutation = useMutation({
    mutationKey: ['receiving', 'table', 'scan-sku'],
    mutationFn: scanSku,
    retry: false,
  })
  const scanQrMutation = useMutation({
    mutationKey: ['receiving', 'table', 'scan-qr'],
    mutationFn: scanQr,
    retry: false,
  })
  const closeBoxMutation = useMutation({
    mutationKey: ['receiving', 'table', 'close-box'],
    mutationFn: closeBox,
    retry: false,
  })
  const scanBufferMutation = useMutation({
    mutationKey: ['receiving', 'table', 'scan-buffer'],
    mutationFn: scanBuffer,
    retry: false,
  })
  const closeCargoplaceMutation = useMutation({
    mutationKey: ['receiving', 'table', 'close-cargoplace'],
    mutationFn: closeCargoplace,
    retry: false,
  })

  const submitCargoplace = async (cargoplaceId: string) => {
    if (scanCargoplaceMutation.isPending) {
      return
    }

    try {
      const result = await scanCargoplaceMutation.mutateAsync(cargoplaceId)
      dispatch({ type: 'CARGOPLACE_OPENED', result })
    } catch (error) {
      dispatch(errorAction(error))
    }
  }

  const submitBox = async (boxBarcode: string) => {
    // Guard against scanner re-entrancy: a keyboard-wedge scanner fires the
    // form submit on Enter, bypassing the disabled button.
    if (!state.cargoplace || scanBoxMutation.isPending) {
      return
    }

    try {
      const result = await scanBoxMutation.mutateAsync({
        cargoplaceId: state.cargoplace.cargoplaceId,
        boxBarcode,
      })
      dispatch({ type: 'BOX_OPENED', result })
    } catch (error) {
      dispatch(errorAction(error))
    }
  }

  const submitBarcode = async (barcode: string) => {
    if (!state.cargoplace || !state.box || scanSkuMutation.isPending) {
      return
    }

    try {
      const result = await scanSkuMutation.mutateAsync({
        cargoplaceId: state.cargoplace.cargoplaceId,
        boxId: state.box.boxId,
        barcode,
      })
      dispatch({ type: 'SKU_RESOLVED', result })
    } catch (error) {
      dispatch(errorAction(error))
    }
  }

  const submitQr = async (qrCode: string) => {
    if (
      !state.cargoplace ||
      !state.box ||
      !state.resolvedSku ||
      scanQrMutation.isPending
    ) {
      return
    }

    try {
      const result = await scanQrMutation.mutateAsync({
        cargoplaceId: state.cargoplace.cargoplaceId,
        boxId: state.box.boxId,
        skuId: state.resolvedSku.skuId,
        qrCode,
      })
      dispatch({ type: 'PRODUCT_REGISTERED', result })
    } catch (error) {
      dispatch(errorAction(error))
    }
  }

  const confirmCloseBox = async () => {
    if (!state.box || closeBoxMutation.isPending) {
      return
    }

    try {
      await closeBoxMutation.mutateAsync(state.box.boxId)
      dispatch({ type: 'BOX_CLOSED' })
    } catch (error) {
      dispatch(errorAction(error))
    }
  }

  const submitBuffer = async (bufferBinId: string) => {
    if (!state.cargoplace || scanBufferMutation.isPending) {
      return
    }

    try {
      const result = await scanBufferMutation.mutateAsync({
        cargoplaceId: state.cargoplace.cargoplaceId,
        bufferBinId,
      })
      dispatch({ type: 'BUFFER_SCANNED', productsPlaced: result.productsPlaced })
    } catch (error) {
      dispatch(errorAction(error))
    }
  }

  const submitCloseCargoplace = async () => {
    if (!state.cargoplace || closeCargoplaceMutation.isPending) {
      return
    }

    try {
      const result = await closeCargoplaceMutation.mutateAsync(
        state.cargoplace.cargoplaceId,
      )
      dispatch({ type: 'CARGOPLACE_CLOSED', summary: result.summary })
    } catch (error) {
      dispatch(errorAction(error))
    }
  }

  return {
    state,
    submitCargoplace,
    submitBox,
    submitBarcode,
    submitQr,
    confirmCloseBox,
    submitBuffer,
    submitCloseCargoplace,
    openConfirmCloseBox: () => dispatch({ type: 'OPEN_CONFIRM_CLOSE_BOX' }),
    cancelConfirmCloseBox: () => dispatch({ type: 'CANCEL_CONFIRM_CLOSE_BOX' }),
    openConfirmAcceptCargoplace: () =>
      dispatch({ type: 'OPEN_CONFIRM_ACCEPT_CARGOPLACE' }),
    cancelConfirmAcceptCargoplace: () =>
      dispatch({ type: 'CANCEL_CONFIRM_ACCEPT_CARGOPLACE' }),
    confirmAcceptCargoplace: () => dispatch({ type: 'GO_TO_BUFFER' }),
    finish: () => dispatch({ type: 'FINISH' }),
    dismissError: () => dispatch({ type: 'DISMISS_ERROR' }),
    isScanningCargoplace: scanCargoplaceMutation.isPending,
    isScanningBox: scanBoxMutation.isPending,
    isScanningSku: scanSkuMutation.isPending,
    isScanningQr: scanQrMutation.isPending,
    isClosingBox: closeBoxMutation.isPending,
    isScanningBuffer: scanBufferMutation.isPending,
    isClosingCargoplace: closeCargoplaceMutation.isPending,
  }
}
