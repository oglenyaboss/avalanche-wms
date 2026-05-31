import type { CloseCargoplaceSummary } from '@/entities/cargoplace'
import { Alert, AlertDescription, Button } from '@/shared/ui'

import { bufferUuidSchema } from '../model/schema'
import type { ActiveCargoplace } from '../model/tableReducer'
import { CargoplaceSummary } from './CargoplaceSummary'
import { ContextSummary } from './ContextSummary'
import { ScanForm } from './ScanForm'

interface BufferPhaseProps {
  cargoplace: ActiveCargoplace
  bufferPlaced: number | null
  summary: CloseCargoplaceSummary | null
  onScanBuffer: (value: string) => Promise<void> | void
  onCloseCargoplace: () => void
  onFinish: () => void
  isScanning: boolean
  isClosing: boolean
  isBlocked: boolean
}

// Three sequential steps, decoupled so a failure in one is independently
// recoverable: scan the buffer bin → finalize the cargoplace → review summary.
export function BufferPhase({
  cargoplace,
  bufferPlaced,
  summary,
  onScanBuffer,
  onCloseCargoplace,
  onFinish,
  isScanning,
  isClosing,
  isBlocked,
}: BufferPhaseProps) {
  // Step 3: the cargoplace is closed — show the received/shortage summary.
  if (summary) {
    return (
      <section className="flex flex-col gap-6">
        <ContextSummary cargoplaceCode={cargoplace.cargoplaceCode} />
        <CargoplaceSummary summary={summary} />
        <div>
          <Button type="button" onClick={onFinish}>
            Понятно
          </Button>
        </div>
      </section>
    )
  }

  // Step 2: products are in the buffer — finalize the cargoplace.
  if (bufferPlaced !== null) {
    return (
      <section className="flex flex-col gap-6">
        <ContextSummary cargoplaceCode={cargoplace.cargoplaceCode} />
        <Alert variant="success">
          <AlertDescription className="col-span-2">
            Товаров размещено в буфере: {bufferPlaced}.
          </AlertDescription>
        </Alert>
        <div>
          <Button type="button" onClick={onCloseCargoplace} disabled={isClosing}>
            {isClosing ? 'Завершение...' : 'Завершить грузоместо'}
          </Button>
        </div>
      </section>
    )
  }

  // Step 1: scan the buffer bin to move the products.
  return (
    <section className="flex flex-col gap-6">
      <ContextSummary cargoplaceCode={cargoplace.cargoplaceCode} />
      <ScanForm
        title="Отсканируйте штрих-код буфера, куда положили товар"
        inputId="buffer-id"
        srLabel="Идентификатор ячейки буфера"
        placeholder="Данные со штрих-кода"
        submitLabel="Принять товар"
        onScan={onScanBuffer}
        isScanning={isScanning}
        isBlocked={isBlocked}
        schema={bufferUuidSchema}
      />
    </section>
  )
}
