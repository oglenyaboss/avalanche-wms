import type { PickedProduct } from '@/entities/putaway'
import { Button } from '@/shared/ui'

import { productQrSchema } from '../model/schema'
import type { ActiveBuffer } from '../model/putawayReducer'
import { BagIcon } from './icons'
import { ScanForm } from './ScanForm'

interface PickPhaseProps {
  buffer: ActiveBuffer
  cart: PickedProduct[]
  cartSize: number | null
  onScan: (value: string) => Promise<void> | void
  onOpenDrafter: () => void
  onGoToStorage: () => void
  isScanning: boolean
  isBlocked: boolean
}

// Step 2 of putaway: scan each product's QR to add it to the putaway task.
// Picked items accumulate in the cart (shown in the Drafter drawer); when the
// operator has collected everything they want, they advance to storage.
export function PickPhase({
  buffer,
  cart,
  cartSize,
  onScan,
  onOpenDrafter,
  onGoToStorage,
  isScanning,
  isBlocked,
}: PickPhaseProps) {
  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-medium">
          Буфер <span className="font-semibold">{buffer.bufferCode}</span>
        </h2>
        <Button
          type="button"
          onClick={onGoToStorage}
          disabled={cart.length === 0 || isScanning}
        >
          Перейти к раскладке
        </Button>
      </div>

      <ScanForm
        title="Отсканируйте QR товара, добавляемого в задачу раскладки"
        inputId="putaway-pick"
        srLabel="QR-код товара"
        placeholder="Данные с QR-кода"
        submitLabel="Добавить"
        manualHint="В случае неполадки сканирования, введите QR вручную"
        onScan={onScan}
        isScanning={isScanning}
        isBlocked={isBlocked}
        schema={productQrSchema}
      />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm font-medium text-muted-foreground">
          Взято: {cart.length}
          {cartSize !== null ? ` · в буфере: ${cartSize}` : ''}
        </p>
        <Button type="button" variant="outline" size="sm" onClick={onOpenDrafter}>
          <BagIcon className="size-4" />
          Товары на раскладку ({cart.length})
        </Button>
      </div>
    </section>
  )
}
