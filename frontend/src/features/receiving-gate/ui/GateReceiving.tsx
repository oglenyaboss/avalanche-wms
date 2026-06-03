import { ScanScene } from '@/shared/ui'

import { useGateReceiving } from '../model/useGateReceiving'
import { ConfirmCloseDialog } from './ConfirmCloseDialog'
import { ReceivingErrorDialog } from './ReceivingErrorDialog'
import { ScanTtnForm } from './ScanTtnForm'
import { ShipmentReceiving } from './ShipmentReceiving'

export function GateReceiving() {
  const gate = useGateReceiving()
  const { state } = gate
  const isErrorOpen = state.errorMessage !== null

  // Persistent live region: the success/close message, or the running gate
  // progress so each scan is announced to screen-reader users.
  const announcement =
    state.successMessage ??
    (state.shipment
      ? `Принято ${state.shipment.progress.received} из ${state.shipment.progress.total}`
      : '')

  return (
    <div className="w-full">
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>

      {state.phase === 'shipment' && state.shipment ? (
        <ShipmentReceiving
          shipment={state.shipment}
          onScanCargoplace={gate.submitCargoplace}
          onAcceptShipment={gate.openConfirmClose}
          isScanning={gate.isScanningCargoplace}
          isErrorOpen={isErrorOpen}
        />
      ) : (
        <ScanScene
          title="Приёмка на воротах"
          description="Отсканируйте ТТН, чтобы открыть поставку и принять грузоместа от перевозчика."
        >
          <ScanTtnForm
            onScan={gate.submitTtn}
            isScanning={gate.isScanningTtn}
            successMessage={state.successMessage}
            isBlocked={isErrorOpen}
          />
        </ScanScene>
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
