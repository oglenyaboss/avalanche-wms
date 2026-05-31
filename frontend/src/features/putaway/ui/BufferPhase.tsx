import { Alert, AlertDescription } from '@/shared/ui'

import { bufferUuidSchema } from '../model/schema'
import { ScanForm } from './ScanForm'

interface BufferPhaseProps {
  onScan: (value: string) => Promise<void> | void
  isScanning: boolean
  isBlocked: boolean
  successMessage: string | null
}

// Step 1 of putaway: scan the receiving-buffer bin to load its RECEIVED
// products. Also the resting state after a task is finished, where the success
// summary is shown above the next scan.
export function BufferPhase({
  onScan,
  isScanning,
  isBlocked,
  successMessage,
}: BufferPhaseProps) {
  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      {successMessage ? (
        <Alert variant="success">
          <AlertDescription>{successMessage}</AlertDescription>
        </Alert>
      ) : null}
      <ScanForm
        title="Отсканируйте штрих-код буфера приёмки"
        inputId="putaway-buffer"
        srLabel="Идентификатор буфера приёмки"
        placeholder="Данные со штрих-кода"
        submitLabel="Подтвердить"
        onScan={onScan}
        isScanning={isScanning}
        isBlocked={isBlocked}
        schema={bufferUuidSchema}
      />
    </section>
  )
}
