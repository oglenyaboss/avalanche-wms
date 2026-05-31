import { usePutaway } from '../model/usePutaway'
import { BufferPhase } from './BufferPhase'
import { ConfirmFinishTaskDialog } from './ConfirmFinishTaskDialog'
import { DrafterSheet } from './DrafterSheet'
import { PickPhase } from './PickPhase'
import { PutawayErrorDialog } from './PutawayErrorDialog'
import { StoragePhase } from './StoragePhase'

export function Putaway() {
  const putaway = usePutaway()
  const { state } = putaway
  const isErrorOpen = state.errorMessage !== null

  // Persistent live region: announce the running placed count (or the finish
  // summary) to screen-reader users.
  const announcement =
    state.successMessage ??
    (state.phase === 'storage'
      ? `Размещено ${state.placedProductIds.length} из ${state.cart.length}`
      : state.phase === 'pick'
        ? `В задаче ${state.cart.length} товаров`
        : '')

  return (
    <div className="mx-auto w-full max-w-5xl">
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>

      {state.phase === 'buffer' || !state.buffer ? (
        <BufferPhase
          onScan={putaway.submitBuffer}
          isScanning={putaway.isScanningBuffer}
          isBlocked={isErrorOpen}
          successMessage={state.successMessage}
        />
      ) : state.phase === 'pick' ? (
        <PickPhase
          buffer={state.buffer}
          cart={state.cart}
          cartSize={state.cartSize}
          onScan={putaway.submitPick}
          onOpenDrafter={putaway.openDrafter}
          onGoToStorage={putaway.goToStorage}
          isScanning={putaway.isScanningProduct}
          isBlocked={isErrorOpen}
        />
      ) : state.phase === 'storage' ? (
        <StoragePhase
          buffer={state.buffer}
          cart={state.cart}
          placedProductIds={state.placedProductIds}
          storageStep={state.storageStep}
          activeBin={state.activeBin}
          lastPlacement={state.lastPlacement}
          onScanBin={putaway.submitStorageBin}
          onScanProduct={putaway.submitPlace}
          onChangeBin={putaway.changeBin}
          onFinish={putaway.openConfirmFinish}
          isPlacing={putaway.isPlacing}
          isBlocked={isErrorOpen}
        />
      ) : null}

      <DrafterSheet
        open={state.drafterOpen}
        onOpenChange={(open) =>
          open ? putaway.openDrafter() : putaway.closeDrafter()
        }
        cart={state.cart}
        placedProductIds={state.placedProductIds}
      />

      <PutawayErrorDialog
        message={state.errorMessage}
        onDismiss={putaway.dismissError}
      />
      <ConfirmFinishTaskDialog
        open={state.confirmFinishOpen}
        onConfirm={putaway.finishTask}
        onCancel={putaway.cancelConfirmFinish}
      />
    </div>
  )
}
