import type { Destination } from '@/entities/assembly'
import { Button } from '@/shared/ui'

import { shippingBufferUuidSchema } from '../model/schema'
import { BagIcon } from './icons'
import { ScanForm } from './ScanForm'

interface BufferPhaseProps {
  destination: Destination
  cartCount: number
  onScan: (value: string) => Promise<void> | void
  onBackToPick: () => void
  onOpenDrafter: () => void
  onFinish: () => void
  isPlacing: boolean
  isBlocked: boolean
}

// Step 3 of assembly: scan the shipping-buffer bin once. That single scan moves
// the operator's whole cart into the buffer and finishes the task, so there is
// no per-product loop here. "Прервать сборку" is an explicit abandon for an
// operator who does NOT scan a buffer — it clears the local batch without
// claiming the task is done (the picks stay ASSEMBLED server-side).
export function BufferPhase({
  destination,
  cartCount,
  onScan,
  onBackToPick,
  onOpenDrafter,
  onFinish,
  isPlacing,
  isBlocked,
}: BufferPhaseProps) {
  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <h2 className="text-lg font-semibold">
            Сборка для магазина {destination.name}
          </h2>
          <p className="text-sm text-muted-foreground">Собрано: {cartCount}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onOpenDrafter}
          >
            <BagIcon className="size-4" />
            Товары к сборке ({cartCount})
          </Button>
          <Button type="button" variant="outline" onClick={onFinish}>
            Прервать сборку
          </Button>
        </div>
      </div>

      <div className="flex flex-col gap-3">
        <ScanForm
          title="Отсканируйте ШК буфера отгрузки, в который складывается сборка"
          inputId="assembly-shipping-buffer"
          srLabel="Идентификатор буфера отгрузки"
          placeholder="Данные со штрих-кода"
          submitLabel="Разместить"
          onScan={onScan}
          isScanning={isPlacing}
          isBlocked={isBlocked}
          schema={shippingBufferUuidSchema}
        />
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="self-start"
          onClick={onBackToPick}
          disabled={isPlacing}
        >
          ← Вернуться к сборке
        </Button>
      </div>
    </section>
  )
}
