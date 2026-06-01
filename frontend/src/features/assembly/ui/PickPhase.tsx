import type { Destination, PickedProduct } from '@/entities/assembly'
import { Button } from '@/shared/ui'

import { productQrSchema } from '../model/schema'
import { BagIcon } from './icons'
import { ScanForm } from './ScanForm'

interface PickPhaseProps {
  destination: Destination
  cart: PickedProduct[]
  taskCount: number
  onScan: (value: string) => Promise<void> | void
  onOpenDrafter: () => void
  onGoToBuffer: () => void
  isScanning: boolean
  isBlocked: boolean
}

// Step 2 of assembly: scan each product's QR to take it from its storage bin
// into the operator's cart. Picked items accumulate in the cart (shown in the
// Drafter drawer); when the operator has collected everything, they move the
// whole cart to the shipping buffer.
export function PickPhase({
  destination,
  cart,
  taskCount,
  onScan,
  onOpenDrafter,
  onGoToBuffer,
  isScanning,
  isBlocked,
}: PickPhaseProps) {
  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-medium">
          Сборка для магазина{' '}
          <span className="font-semibold">{destination.name}</span>
        </h2>
        <Button
          type="button"
          onClick={onGoToBuffer}
          disabled={cart.length === 0 || isScanning}
        >
          Перейти к буферу отгрузки
        </Button>
      </div>

      <ScanForm
        title="Отсканируйте QR товара, добавляемого в сборку"
        inputId="assembly-pick"
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
          Взято: {cart.length} · Задач: {taskCount}
        </p>
        <Button type="button" variant="outline" size="sm" onClick={onOpenDrafter}>
          <BagIcon className="size-4" />
          Товары к сборке ({cart.length})
        </Button>
      </div>
    </section>
  )
}
