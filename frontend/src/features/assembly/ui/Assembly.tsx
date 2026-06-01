import { useAssembly } from '../model/useAssembly'
import { AssemblyErrorDialog } from './AssemblyErrorDialog'
import { BufferPhase } from './BufferPhase'
import { ConfirmFinishTaskDialog } from './ConfirmFinishTaskDialog'
import { DestinationPhase } from './DestinationPhase'
import { DrafterSheet } from './DrafterSheet'
import { PickPhase } from './PickPhase'

export function Assembly() {
  const assembly = useAssembly()
  const { state } = assembly
  const isErrorOpen = state.errorMessage !== null

  // Persistent live region: announce the running cart count (or the finish
  // summary) to screen-reader users.
  const announcement =
    state.successMessage ??
    (state.phase === 'pick'
      ? `В сборке ${state.cart.length} товаров`
      : state.phase === 'buffer'
        ? `Собрано ${state.cart.length} товаров`
        : '')

  return (
    <div className="mx-auto w-full max-w-5xl">
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>

      {state.phase === 'destination' || !state.selectedDestination ? (
        <DestinationPhase
          destinations={state.destinations}
          selectedDestination={state.selectedDestination}
          allocation={state.allocation}
          tasks={state.tasks}
          successMessage={state.successMessage}
          onSelect={assembly.selectDestination}
          onDeselect={assembly.deselect}
          onAllocate={assembly.allocate}
          onStartPicking={assembly.startPicking}
          isLoadingDestinations={assembly.isLoadingDestinations}
          isAllocating={assembly.isAllocating}
          isLoadingTasks={assembly.isLoadingTasks}
        />
      ) : state.phase === 'pick' ? (
        <PickPhase
          destination={state.selectedDestination}
          cart={state.cart}
          taskCount={state.tasks.length}
          onScan={assembly.submitPick}
          onOpenDrafter={assembly.openDrafter}
          onGoToBuffer={assembly.goToBuffer}
          isScanning={assembly.isPicking}
          isBlocked={isErrorOpen}
        />
      ) : state.phase === 'buffer' ? (
        <BufferPhase
          destination={state.selectedDestination}
          cartCount={state.cart.length}
          onScan={assembly.submitBuffer}
          onBackToPick={assembly.backToPick}
          onFinish={assembly.openConfirmFinish}
          isPlacing={assembly.isPlacing}
          isBlocked={isErrorOpen}
        />
      ) : null}

      <DrafterSheet
        open={state.drafterOpen}
        onOpenChange={(open) =>
          open ? assembly.openDrafter() : assembly.closeDrafter()
        }
        cart={state.cart}
      />

      <AssemblyErrorDialog
        message={state.errorMessage}
        onDismiss={assembly.dismissError}
      />
      <ConfirmFinishTaskDialog
        open={state.confirmFinishOpen}
        onConfirm={assembly.finishTask}
        onCancel={assembly.cancelConfirmFinish}
      />
    </div>
  )
}
