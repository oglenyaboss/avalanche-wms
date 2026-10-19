import type { PickedProduct, StoragePlacement } from '@/entities/putaway'
import { OperationChainStatus } from '@/entities/signed-action'
import { Alert, AlertDescription, Button } from '@/shared/ui'

import type { ActiveBuffer } from '../model/putawayReducer'
import { storageBinUuidSchema } from '../model/schema'
import { ScanForm } from './ScanForm'

interface StoragePhaseProps {
  buffer: ActiveBuffer
  cart: PickedProduct[]
  placedProductIds: string[]
  lastPlacement: StoragePlacement | null
  onScanBin: (value: string) => Promise<void> | void
  onFinish: () => void
  isPlacing: boolean
  isBlocked: boolean
}

// Step 3 of putaway: scanning a storage bin places the whole unplaced cart
// into it as one signed action. The task is finished from here as before.
export function StoragePhase({
  buffer,
  cart,
  placedProductIds,
  lastPlacement,
  onScanBin,
  onFinish,
  isPlacing,
  isBlocked,
}: StoragePhaseProps) {
  const placedCount = placedProductIds.length
  const taskLabel = `Раскладка из буфера ${buffer.bufferCode}`

  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <h2 className="text-lg font-semibold">{taskLabel}</h2>
          <p className="text-sm text-muted-foreground">
            Размещено: {placedCount} из {cart.length}
          </p>
        </div>
        <Button type="button" variant="outline" onClick={onFinish}>
          Завершить задачу
        </Button>
      </div>

      {lastPlacement ? (
        <div className="flex flex-col gap-3">
          <Alert variant="success">
            <AlertDescription>
              Размещено в ячейку {lastPlacement.storageBinCode}:{' '}
              {lastPlacement.productsPlaced} товаров
            </AlertDescription>
          </Alert>
          <OperationChainStatus operationId={lastPlacement.operationId} />
        </div>
      ) : null}

      {placedCount < cart.length ? (
        <ScanForm
          key="storage-bin"
          title="Отсканируйте ШК ячейки, в которую хотите положить товар"
          inputId="putaway-storage-bin"
          srLabel="Идентификатор ячейки хранения"
          placeholder="Данные со штрих-кода"
          submitLabel="Далее"
          onScan={onScanBin}
          isScanning={isPlacing}
          isBlocked={isBlocked}
          schema={storageBinUuidSchema}
        />
      ) : null}
    </section>
  )
}
