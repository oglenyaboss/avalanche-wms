import { useReducer } from 'react'
import { useMutation } from '@tanstack/react-query'

import { scanBuffer, scanDriver, ship } from '../api/shippingApi'
import { getShippingErrorMessage } from './errors'
import { initialShippingState, shippingReducer } from './shippingReducer'

type ShipMode = 'bulk' | 'spot'

export function useShipping() {
  const [state, dispatch] = useReducer(shippingReducer, initialShippingState)

  const scanBufferMutation = useMutation({
    mutationKey: ['shipping', 'scan-buffer'],
    mutationFn: scanBuffer,
    retry: false,
  })
  const scanDriverMutation = useMutation({
    mutationKey: ['shipping', 'scan-driver'],
    mutationFn: scanDriver,
    retry: false,
  })
  const shipMutation = useMutation({
    mutationKey: ['shipping', 'ship'],
    mutationFn: ship,
    retry: false,
  })

  const showError = (message: string) =>
    dispatch({ type: 'SHOW_ERROR', message })

  const submitBufferScan = async (bufferBinId: string) => {
    // Guard scanner re-entrancy: a keyboard-wedge scanner fires submit on Enter,
    // bypassing the disabled button.
    if (state.phase !== 'buffer' || scanBufferMutation.isPending) {
      return
    }

    try {
      const result = await scanBufferMutation.mutateAsync(bufferBinId)
      dispatch({ type: 'BUFFER_SCANNED', result })
    } catch (error) {
      showError(getShippingErrorMessage(error))
    }
  }

  const submitDriverScan = async (dispatchCode: string) => {
    if (state.phase !== 'driver' || scanDriverMutation.isPending) {
      return
    }

    try {
      const result = await scanDriverMutation.mutateAsync(dispatchCode)
      dispatch({ type: 'DRIVER_SCANNED', result })
    } catch (error) {
      showError(getShippingErrorMessage(error))
    }
  }

  // Resolve a scanned product QR to a buffer product and add it to the spot
  // selection. A QR not in the current buffer is rejected client-side so it can
  // never reach the ship request.
  const scanProduct = (qrCode: string) => {
    if (state.phase !== 'ship') {
      return
    }
    const product = state.products.find((p) => p.qrCode === qrCode)
    if (!product) {
      showError('Этот товар отсутствует в буфере отгрузки.')
      return
    }
    dispatch({ type: 'SELECT_PRODUCT', productId: product.productId })
  }

  const performShip = async (mode: ShipMode) => {
    // The post-partial-ship buffer reload is a second mutation; block a re-entrant
    // ship while it is in flight, otherwise a fast double-tap would ship against a
    // stale (pre-refresh) product list.
    if (
      state.phase !== 'ship' ||
      shipMutation.isPending ||
      scanBufferMutation.isPending ||
      state.destinationMismatch ||
      !state.bufferBin ||
      !state.dispatch
    ) {
      return
    }

    const productIds = mode === 'spot' ? state.selectedProductIds : undefined
    if (mode === 'spot' && (!productIds || productIds.length === 0)) {
      return
    }
    if (mode === 'bulk' && state.products.length === 0) {
      return
    }

    const bufferBinId = state.bufferBin.id

    try {
      const result = await shipMutation.mutateAsync({
        bufferBinId,
        dispatchId: state.dispatch.dispatchId,
        productIds,
      })
      dispatch({ type: 'SHIPPED', result })
      // Partial ship: the dispatch stays AT_GATE and more items remain. Reload the
      // buffer (read-only, idempotent) so the list reflects what is still ready.
      if (!result.dispatchDeparted) {
        const refreshed = await scanBufferMutation.mutateAsync(bufferBinId)
        dispatch({ type: 'BUFFER_REFRESHED', result: refreshed })
      }
    } catch (error) {
      showError(getShippingErrorMessage(error))
    }
  }

  return {
    state,
    submitBufferScan,
    submitDriverScan,
    scanProduct,
    toggleProduct: (productId: string) =>
      dispatch({ type: 'TOGGLE_PRODUCT', productId }),
    clearSelection: () => dispatch({ type: 'CLEAR_SELECTION' }),
    shipBulk: () => performShip('bulk'),
    shipSpot: () => performShip('spot'),
    backToBuffer: () => dispatch({ type: 'BACK_TO_BUFFER' }),
    backToDriver: () => dispatch({ type: 'BACK_TO_DRIVER' }),
    reset: () => dispatch({ type: 'RESET' }),
    dismissError: () => dispatch({ type: 'DISMISS_ERROR' }),
    isScanningBuffer: scanBufferMutation.isPending,
    isScanningDriver: scanDriverMutation.isPending,
    // Keep the ship controls disabled through the post-partial-ship buffer reload,
    // not just the /ship call, so they cannot re-fire against a stale list.
    isShipping: shipMutation.isPending || scanBufferMutation.isPending,
  }
}
