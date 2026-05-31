import { useGateReceiving } from '../model/useGateReceiving'
import { ConfirmCloseDialog } from './ConfirmCloseDialog'
import { ReceivingErrorDialog } from './ReceivingErrorDialog'
import { ScanTtnForm } from './ScanTtnForm'
import { ShipmentReceiving } from './ShipmentReceiving'

export function GateReceiving() {
  const gate = useGateReceiving()
  const { state } = gate
  const isErrorOpen = state.errorMessage !== null

  return (
    <div className="mx-auto w-full max-w-2xl">
      {state.phase === 'shipment' && state.shipment ? (
        <ShipmentReceiving
          shipment={state.shipment}
          onScanCargoplace={gate.submitCargoplace}
          onAcceptShipment={gate.openConfirmClose}
          isScanning={gate.isScanningCargoplace}
          isErrorOpen={isErrorOpen}
        />
      ) : (
        <ScanTtnForm
          onScan={gate.submitTtn}
          isScanning={gate.isScanningTtn}
          successMessage={state.successMessage}
        />
      )}

      <ReceivingErrorDialog
        message={state.errorMessage}
        onDismiss={gate.dismissError}
      />
      <ConfirmCloseDialog
        open={state.confirmCloseOpen}
        isPending={gate.isClosing}
        onConfirm={gate.confirmClose}
        onCancel={gate.cancelConfirmClose}
      />
    </div>
  )
}
