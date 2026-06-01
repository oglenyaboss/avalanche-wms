import { Alert, AlertDescription } from '@/shared/ui'

import { bufferBinUuidSchema } from '../model/schema'
import { ScanForm } from './ScanForm'

interface BufferScanPhaseProps {
  emptyBufferMessage: string | null
  onScan: (value: string) => Promise<void> | void
  isScanning: boolean
  isBlocked: boolean
}

// Step 1 of shipping: scan the store's SHIPPING_BUFFER bin to inspect what is
// ready to load. Read-only — it just opens the buffer.
export function BufferScanPhase({
  emptyBufferMessage,
  onScan,
  isScanning,
  isBlocked,
}: BufferScanPhaseProps) {
  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      <h2 className="text-lg font-medium">Отсканируйте буфер отгрузки магазина</h2>

      {emptyBufferMessage ? (
        <Alert variant="default">
          <AlertDescription>{emptyBufferMessage}</AlertDescription>
        </Alert>
      ) : null}

      <ScanForm
        title="Отсканируйте ШК буфера отгрузки"
        inputId="shipping-buffer"
        srLabel="Идентификатор буфера отгрузки"
        placeholder="Данные со штрих-кода"
        submitLabel="Осмотреть буфер"
        manualHint="В случае неполадки сканирования, введите код буфера вручную"
        onScan={onScan}
        isScanning={isScanning}
        isBlocked={isBlocked}
        schema={bufferBinUuidSchema}
      />
    </section>
  )
}
