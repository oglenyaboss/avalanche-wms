import type { DispatchInfo, ShipResult } from '@/entities/shipping'
import { Button, Card, CardContent } from '@/shared/ui'

import { CheckCircleIcon } from './icons'

interface DonePhaseProps {
  dispatch: DispatchInfo
  result: ShipResult
  onReset: () => void
}

interface SummaryRowProps {
  label: string
  value: number
}

function SummaryRow({ label, value }: SummaryRowProps) {
  return (
    <div className="flex items-center justify-between gap-4 py-2">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="text-lg font-semibold tabular-nums">{value}</span>
    </div>
  )
}

// Terminal phase: the buffer emptied and the truck departed. Show a summary and
// offer a fresh shipment.
export function DonePhase({ dispatch, result, onReset }: DonePhaseProps) {
  return (
    <section className="mx-auto flex max-w-md flex-col items-center gap-6 text-center">
      <span className="flex size-16 items-center justify-center rounded-full bg-accent text-accent-foreground">
        <CheckCircleIcon className="size-8" />
      </span>

      <div className="flex flex-col gap-1">
        <h2 className="text-xl font-semibold">Отгрузка завершена</h2>
        <p className="text-sm text-muted-foreground">
          Машина «{dispatch.vehicleNumber}» отбыла со склада. Буфер «
          {dispatch.destination.name}» пуст.
        </p>
      </div>

      <Card className="w-full text-left">
        <CardContent className="flex flex-col divide-y divide-border px-6 py-2">
          <SummaryRow label="Отгружено товаров" value={result.productsShipped} />
          <SummaryRow label="Заказов завершено" value={result.ordersCompleted} />
          <SummaryRow
            label="Заказов отгружено частично"
            value={result.ordersPartiallyShipped}
          />
          <SummaryRow
            label="Событий зафиксировано"
            value={result.outboxEventsCreated}
          />
        </CardContent>
      </Card>

      <Button type="button" className="w-full" onClick={onReset}>
        Новая отгрузка
      </Button>
    </section>
  )
}
