import { useEffect, useReducer } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'

import type { Destination } from '@/entities/assembly'

import {
  allocate,
  getDestinations,
  getTasks,
  pick,
  scanShippingBuffer,
} from '../api/assemblyApi'
import { getAssemblyErrorMessage } from './errors'
import { assemblyReducer, initialAssemblyState } from './assemblyReducer'

export function useAssembly() {
  const [state, dispatch] = useReducer(assemblyReducer, initialAssemblyState)

  // Destinations load once on mount. useQuery has no onSuccess in v5, so the
  // DESTINATIONS_LOADED dispatch is driven by an effect keyed on the data.
  const destinationsQuery = useQuery({
    queryKey: ['assembly', 'destinations'],
    queryFn: getDestinations,
    retry: false,
  })

  useEffect(() => {
    if (destinationsQuery.data) {
      dispatch({ type: 'DESTINATIONS_LOADED', destinations: destinationsQuery.data })
    }
  }, [destinationsQuery.data])

  // Surface a failed destinations load (403 / network / 5xx) through the same
  // error dialog every request uses, instead of silently rendering an empty
  // store list. dispatch is stable, so it stays out of the dep array.
  useEffect(() => {
    if (destinationsQuery.error) {
      dispatch({
        type: 'SHOW_ERROR',
        message: getAssemblyErrorMessage(destinationsQuery.error),
      })
    }
  }, [destinationsQuery.error])

  const allocateMutation = useMutation({
    mutationKey: ['assembly', 'allocate'],
    mutationFn: allocate,
    retry: false,
  })
  const tasksMutation = useMutation({
    mutationKey: ['assembly', 'tasks'],
    mutationFn: getTasks,
    retry: false,
  })
  const pickMutation = useMutation({
    mutationKey: ['assembly', 'pick'],
    mutationFn: pick,
    retry: false,
  })
  const scanBufferMutation = useMutation({
    mutationKey: ['assembly', 'scan-shipping-buffer'],
    mutationFn: scanShippingBuffer,
    retry: false,
  })

  const showError = (message: string) =>
    dispatch({ type: 'SHOW_ERROR', message })

  // Resolve a scanned QR to a pick task. Returns null when the code is not among
  // the loaded PENDING tasks for the selected destination.
  const resolveQr = (qrCode: string) =>
    state.tasks.find((task) => task.qrCode === qrCode) ?? null

  // Load PENDING tasks for the selected destination via the tasks mutation.
  const loadTasks = async (destinationId: string) => {
    try {
      const tasks = await tasksMutation.mutateAsync(destinationId)
      dispatch({ type: 'TASKS_LOADED', destinationId, tasks })
    } catch (error) {
      showError(getAssemblyErrorMessage(error))
    }
  }

  // Selecting a store loads its pick tasks immediately so the summary shows a
  // count even before allocate runs.
  const selectDestination = (destination: Destination) => {
    dispatch({ type: 'DESTINATION_SELECTED', destination })
    void loadTasks(destination.destinationId)
  }

  const deselect = () => dispatch({ type: 'DESELECT_DESTINATION' })

  const runAllocate = async () => {
    if (!state.selectedDestination || allocateMutation.isPending) {
      return
    }

    const destinationId = state.selectedDestination.destinationId
    try {
      const result = await allocateMutation.mutateAsync(destinationId)
      dispatch({ type: 'ALLOCATED', destinationId, result })
      // Allocation reserved new ALLOCATED products — refetch tasks to pick them.
      await loadTasks(destinationId)
    } catch (error) {
      showError(getAssemblyErrorMessage(error))
    }
  }

  const refreshTasks = () => {
    if (!state.selectedDestination) {
      return
    }
    void loadTasks(state.selectedDestination.destinationId)
  }

  const startPicking = () => dispatch({ type: 'START_PICKING' })

  const submitPick = async (qrCode: string) => {
    // Guard against scanner re-entrancy: a keyboard-wedge scanner fires the
    // form submit on Enter, bypassing the disabled button.
    if (state.phase !== 'pick' || pickMutation.isPending) {
      return
    }

    const task = resolveQr(qrCode)
    if (!task) {
      showError('Этот товар отсутствует в задачах сборки.')
      return
    }

    try {
      const result = await pickMutation.mutateAsync(task.productId)
      dispatch({ type: 'PRODUCT_PICKED', result, task })
    } catch (error) {
      showError(getAssemblyErrorMessage(error))
    }
  }

  const goToBuffer = () => dispatch({ type: 'GO_TO_BUFFER' })
  const backToPick = () => dispatch({ type: 'BACK_TO_PICK' })

  const submitBuffer = async (bufferBinId: string) => {
    if (state.phase !== 'buffer' || scanBufferMutation.isPending) {
      return
    }

    const destinationId = state.selectedDestination?.destinationId

    try {
      const result = await scanBufferMutation.mutateAsync(bufferBinId)
      // The batch is placed; BUFFER_SCANNED returns to the store panel keeping
      // the store selected (multi-trip). Reload the remaining PENDING tasks so
      // the operator can keep picking for the same store without re-selecting.
      dispatch({ type: 'BUFFER_SCANNED', result })
      if (destinationId) {
        await loadTasks(destinationId)
      }
    } catch (error) {
      showError(getAssemblyErrorMessage(error))
    }
  }

  return {
    state,
    selectDestination,
    deselect,
    allocate: runAllocate,
    refreshTasks,
    startPicking,
    submitPick,
    goToBuffer,
    backToPick,
    submitBuffer,
    openDrafter: () => dispatch({ type: 'OPEN_DRAFTER' }),
    closeDrafter: () => dispatch({ type: 'CLOSE_DRAFTER' }),
    openConfirmFinish: () => dispatch({ type: 'OPEN_CONFIRM_FINISH' }),
    cancelConfirmFinish: () => dispatch({ type: 'CANCEL_CONFIRM_FINISH' }),
    finishTask: () => dispatch({ type: 'FINISH_TASK' }),
    dismissError: () => dispatch({ type: 'DISMISS_ERROR' }),
    isLoadingDestinations: destinationsQuery.isPending,
    isAllocating: allocateMutation.isPending,
    isLoadingTasks: tasksMutation.isPending,
    isPicking: pickMutation.isPending,
    isPlacing: scanBufferMutation.isPending,
  }
}
