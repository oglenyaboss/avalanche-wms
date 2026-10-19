import { useReducer } from 'react'
import { useMutation } from '@tanstack/react-query'

import { signOperation } from '@/entities/signed-action'
import { webauthnSignErrorText } from '@/shared/lib'

import {
  draftPlaceToStorageBin,
  scanBuffer,
  scanProduct,
  submitPlaceToStorageBin,
} from '../api/putawayApi'
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
  // Scanning the storage bin signs one operation for the whole unplaced cart:
  // draft the batch against the bin, one passkey touch per part, then submit
  // the assertions.
  const placeMutation = useMutation({
    mutationKey: ['putaway', 'place-to-storage-bin'],
    mutationFn: async (input: { productIds: string[]; storageBinId: string }) => {
      const draft = await draftPlaceToStorageBin(input)
      const assertions = await signOperation(draft)
      return submitPlaceToStorageBin(draft.operationId, assertions)
    },
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

  // The scanned bin triggers the whole placement: every cart product not yet
  // placed goes into it as one signed action. A bad bin (STORAGE_BIN_NOT_FOUND)
  // leaves the operator at the bin scan to re-scan.
  const submitStorageBin = async (storageBinId: string) => {
    if (!state.buffer || placeMutation.isPending) {
      return
    }

    const productIds = state.cart
      .filter((item) => !state.placedProductIds.includes(item.productId))
      .map((item) => item.productId)
    if (productIds.length === 0) {
      return
    }

    try {
      const result = await placeMutation.mutateAsync({ productIds, storageBinId })
      dispatch({ type: 'PRODUCTS_PLACED', productIds, result })
    } catch (error) {
      // A DOMException comes from the authenticator (dismissed prompt or
      // timeout) — the placement may not even have been requested, so the
      // putaway error classification does not apply; show the signing text
      // instead.
      const signError = webauthnSignErrorText(error)
      if (signError) {
        showError(signError)
        return
      }

      showError(getPutawayErrorMessage(error))
    }
  }

  return {
    state,
    submitBuffer,
    submitPick,
    goToStorage,
    submitStorageBin,
    openDrafter: () => dispatch({ type: 'OPEN_DRAFTER' }),
    closeDrafter: () => dispatch({ type: 'CLOSE_DRAFTER' }),
    openConfirmFinish: () => dispatch({ type: 'OPEN_CONFIRM_FINISH' }),
    cancelConfirmFinish: () => dispatch({ type: 'CANCEL_CONFIRM_FINISH' }),
    finishTask: () => dispatch({ type: 'FINISH_TASK' }),
    dismissError: () => dispatch({ type: 'DISMISS_ERROR' }),
    isScanningBuffer: scanBufferMutation.isPending,
    isScanningProduct: scanProductMutation.isPending,
    isPlacing: placeMutation.isPending,
  }
}
