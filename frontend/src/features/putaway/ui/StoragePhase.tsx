import type { PickedProduct, StoragePlacement } from '@/entities/putaway'
import { Alert, AlertDescription, Button } from '@/shared/ui'

import type { ActiveBuffer, ActiveStorageBin } from '../model/putawayReducer'
import { productQrSchema, storageBinUuidSchema } from '../model/schema'
import { ScanForm } from './ScanForm'

interface StoragePhaseProps {
  buffer: ActiveBuffer
  cart: PickedProduct[]
  placedProductIds: string[]
  storageStep: 'bin' | 'place'
  activeBin: ActiveStorageBin | null
  lastPlacement: StoragePlacement | null
  onScanBin: (value: string) => Promise<void> | void
  onScanProduct: (value: string) => Promise<void> | void
  onChangeBin: () => void
  onFinish: () => void
  isPlacing: boolean
  isBlocked: boolean
}

// Resolve the human-readable bin code. It only exists after a successful
// placement into the active bin (no resolve endpoint), so before that we show a
// short form of the scanned UUID.
function binLabel(
  activeBin: ActiveStorageBin,
  lastPlacement: StoragePlacement | null,
): string {
  if (lastPlacement && lastPlacement.storageBinId === activeBin.storageBinId) {
    return lastPlacement.storageBinCode
  }
  return `${activeBin.storageBinId.slice(0, 8)}…`
}

// Steps 4–5 of putaway: scan a storage bin, then place picked products into it
// one at a time. The whole task is finished from either step.
export function StoragePhase({
  buffer,
  cart,
  placedProductIds,
  storageStep,
  activeBin,
  lastPlacement,
  onScanBin,
  onScanProduct,
  onChangeBin,
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
          {storageStep === 'place' && activeBin ? (
            <>
              <span className="text-sm text-muted-foreground">{taskLabel}</span>
              <h2 className="text-lg font-semibold">
                Ячейка {binLabel(activeBin, lastPlacement)}
              </h2>
            </>
          ) : (
            <h2 className="text-lg font-semibold">{taskLabel}</h2>
          )}
          <p className="text-sm text-muted-foreground">
            Размещено: {placedCount} из {cart.length}
          </p>
        </div>
        <Button type="button" variant="outline" onClick={onFinish}>
          Завершить задачу
        </Button>
      </div>

      {lastPlacement ? (
        <Alert variant="success">
          <AlertDescription>
            Размещено в ячейку {lastPlacement.storageBinCode}. Всего размещено:{' '}
            {placedCount} из {cart.length}.
          </AlertDescription>
        </Alert>
      ) : null}

      {storageStep === 'bin' || !activeBin ? (
        <ScanForm
          key="storage-bin"
          title="Отсканируйте ШК ячейки, в которую хотите положить товар"
          inputId="putaway-storage-bin"
          srLabel="Идентификатор ячейки хранения"
          placeholder="Данные со штрих-кода"
          submitLabel="Далее"
          onScan={onScanBin}
          isScanning={false}
          isBlocked={isBlocked}
          schema={storageBinUuidSchema}
        />
      ) : (
        <div className="flex flex-col gap-3">
          <ScanForm
            key="storage-place"
            title="Отсканируйте QR-код товара, складываемого в ячейку"
            inputId="putaway-place"
            srLabel="QR-код товара"
            placeholder="Данные с QR-кода"
            submitLabel="Положить"
            manualHint="В случае неполадки сканирования, введите QR вручную"
            onScan={onScanProduct}
            isScanning={isPlacing}
            isBlocked={isBlocked}
            schema={productQrSchema}
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="self-start"
            onClick={onChangeBin}
            disabled={isPlacing}
          >
            Сменить ячейку
          </Button>
        </div>
      )}
    </section>
  )
}
