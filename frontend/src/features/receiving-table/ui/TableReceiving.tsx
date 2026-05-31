import { useTableReceiving } from '../model/useTableReceiving'
import { BoxPhase } from './BoxPhase'
import { BufferPhase } from './BufferPhase'
import { CargoplacePhase } from './CargoplacePhase'
import { ConfirmAcceptCargoplaceDialog } from './ConfirmAcceptCargoplaceDialog'
import { ConfirmCloseBoxDialog } from './ConfirmCloseBoxDialog'
import { ProductPhase } from './ProductPhase'
import { ReceivingErrorDialog } from './ReceivingErrorDialog'

export function TableReceiving() {
  const table = useTableReceiving()
  const { state } = table
  const isErrorOpen = state.errorMessage !== null

  // Persistent live region: the success message, or the running cargoplace
  // progress so each scan is announced to screen-reader users.
  const announcement =
    state.successMessage ??
    (state.cargoplace
      ? `Принято ${state.cargoplace.progress.received} из ${state.cargoplace.progress.expected}`
      : '')

  return (
    <div className="mx-auto w-full max-w-5xl">
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>

      {state.phase === 'cargoplace' || !state.cargoplace ? (
        <CargoplacePhase
          onScan={table.submitCargoplace}
          isScanning={table.isScanningCargoplace}
          isBlocked={isErrorOpen}
          successMessage={state.successMessage}
        />
      ) : state.phase === 'box' ? (
        <BoxPhase
          cargoplace={state.cargoplace}
          scannedProducts={state.scannedProducts}
          onScanBox={table.submitBox}
          onAcceptCargoplace={table.openConfirmAcceptCargoplace}
          isScanning={table.isScanningBox}
          isBlocked={isErrorOpen}
        />
      ) : state.phase === 'product' && state.box ? (
        <ProductPhase
          cargoplace={state.cargoplace}
          box={state.box}
          productStep={state.productStep}
          resolvedSku={state.resolvedSku}
          scannedProducts={state.scannedProducts}
          onScanBarcode={table.submitBarcode}
          onScanQr={table.submitQr}
          onCloseBox={table.openConfirmCloseBox}
          isScanningBarcode={table.isScanningSku}
          isScanningQr={table.isScanningQr}
          isBlocked={isErrorOpen}
        />
      ) : state.phase === 'buffer' ? (
        <BufferPhase
          cargoplace={state.cargoplace}
          bufferPlaced={state.bufferPlaced}
          summary={state.summary}
          onScanBuffer={table.submitBuffer}
          onFinish={table.finishCargoplace}
          onDone={table.finish}
          isScanning={table.isScanningBuffer}
          isClosing={table.isClosingCargoplace}
          isBlocked={isErrorOpen}
        />
      ) : null}

      <ReceivingErrorDialog
        message={state.errorMessage}
        onDismiss={table.dismissError}
      />
      <ConfirmCloseBoxDialog
        open={state.confirmCloseBoxOpen}
        isPending={table.isClosingBox}
        onConfirm={table.confirmCloseBox}
        onCancel={table.cancelConfirmCloseBox}
      />
      <ConfirmAcceptCargoplaceDialog
        open={state.confirmAcceptCargoplaceOpen}
        onConfirm={table.confirmAcceptCargoplace}
        onCancel={table.cancelConfirmAcceptCargoplace}
      />
    </div>
  )
}
