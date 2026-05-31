import { useReducer } from 'react'
import { useMutation } from '@tanstack/react-query'

import { scanBuffer, scanProduct, scanStorageBin } from '../api/putawayApi'
import { getPutawayErrorMessage } from './errors'
import { initialPutawayState, putawayReducer } from './putawayReducer'

export function usePutaway() {
  const [state, dispatch] = useReducer(putawayReducer, initialPutawayState)

  const scanBufferMutation = useMutation({
    mutationKey: ['putaway', 'scan-buffer'],
    mutationFn: scanBuffer,
    retry: false,
  })
  const scanProductMutation = useMutation({
    mutationKey: ['putaway', 'scan-product'],
    mutationFn: scanProduct,
    retry: false,
  })
  const scanStorageBinMutation = useMutation({
    mutationKey: ['putaway', 'scan-storage-bin'],
    mutationFn: scanStorageBin,
    retry: false,
  })

  const showError = (message: string) => dispatch({ type: 'SHOW_ERROR', message })

  // Resolve a scanned QR to a buffer product. Returns null when the code is not
  // among the RECEIVED products the buffer scan returned.
  const resolveQr = (qrCode: string) =>
    state.buffer?.products.find((p) => p.qrCode === qrCode) ?? null

  const submitBuffer = async (bufferBinId: string) => {
    if (scanBufferMutation.isPending) {
      return
    }

    try {
      const result = await scanBufferMutation.mutateAsync(bufferBinId)
      dispatch({ type: 'BUFFER_SCANNED', result })
    } catch (error) {
      showError(getPutawayErrorMessage(error))
    }
  }

  const submitPick = async (qrCode: string) => {
    // Guard against scanner re-entrancy: a keyboard-wedge scanner fires the
    // form submit on Enter, bypassing the disabled button.
    if (!state.buffer || scanProductMutation.isPending) {
      return
    }

    const product = resolveQr(qrCode)
    if (!product) {
      showError('Этот товар отсутствует в буфере приёмки.')
      return
    }

    try {
      const result = await scanProductMutation.mutateAsync({
        productId: product.productId,
        bufferBinId: state.buffer.bufferBinId,
      })
      dispatch({ type: 'PRODUCT_PICKED', result })
    } catch (error) {
      showError(getPutawayErrorMessage(error))
    }
  }

  const goToStorage = () => dispatch({ type: 'GO_TO_STORAGE' })

  // No "resolve bin" endpoint exists: the scanned storage-bin UUID is held
  // client-side and validated when the first product is placed (scan-storage-bin
  // returns STORAGE_BIN_NOT_FOUND for a bad bin).
  const submitStorageBin = (storageBinId: string) =>
    dispatch({ type: 'STORAGE_BIN_SCANNED', storageBinId })

  const changeBin = () => dispatch({ type: 'CHANGE_BIN' })

  const submitPlace = async (qrCode: string) => {
    if (
      !state.buffer ||
      !state.activeBin ||
      scanStorageBinMutation.isPending
    ) {
      return
    }

    const product = resolveQr(qrCode)
    if (!product) {
      showError('Этот товар отсутствует в буфере приёмки.')
      return
    }
    if (!state.cart.some((item) => item.productId === product.productId)) {
      showError('Этот товар не был добавлен в задачу раскладки.')
      return
    }
    if (state.placedProductIds.includes(product.productId)) {
      showError('Этот товар уже размещён в ячейке.')
      return
    }

    try {
      const result = await scanStorageBinMutation.mutateAsync({
        productIds: [product.productId],
        storageBinId: state.activeBin.storageBinId,
      })
      dispatch({ type: 'PRODUCT_PLACED', productId: product.productId, result })
    } catch (error) {
      showError(getPutawayErrorMessage(error))
    }
  }

  return {
    state,
    submitBuffer,
    submitPick,
    goToStorage,
    submitStorageBin,
    changeBin,
    submitPlace,
    openDrafter: () => dispatch({ type: 'OPEN_DRAFTER' }),
    closeDrafter: () => dispatch({ type: 'CLOSE_DRAFTER' }),
    openConfirmFinish: () => dispatch({ type: 'OPEN_CONFIRM_FINISH' }),
    cancelConfirmFinish: () => dispatch({ type: 'CANCEL_CONFIRM_FINISH' }),
    finishTask: () => dispatch({ type: 'FINISH_TASK' }),
    dismissError: () => dispatch({ type: 'DISMISS_ERROR' }),
    isScanningBuffer: scanBufferMutation.isPending,
    isScanningProduct: scanProductMutation.isPending,
    isPlacing: scanStorageBinMutation.isPending,
  }
}
