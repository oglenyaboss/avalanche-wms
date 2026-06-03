import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/shared/ui'

import { formatNumber, lifecycleLabel } from '../model/format'
import type { StatusBucket, SummaryReport } from '../model/types'

interface PipelineCardProps {
  summary: SummaryReport
}

// Canonical product lifecycle order, so the funnel always reads receiving →
// shipping regardless of which statuses currently have stock.
const PRODUCT_ORDER = [
  'RECEIVED',
  'STORED',
  'ALLOCATED',
  'ASSEMBLED',
  'READY_TO_SHIP',
  'SHIPPED',
]

function toMap(buckets: StatusBucket[]): Record<string, number> {
  return Object.fromEntries(buckets.map((b) => [b.status, b.count]))
}

function StatusPills({ buckets }: { buckets: StatusBucket[] }) {
  if (buckets.length === 0) {
    return <span className="text-xs text-muted-foreground">—</span>
  }
  return (
    <div className="flex flex-wrap gap-1.5">
      {buckets.map((b) => (
        <span
          key={b.status}
          className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground"
        >
          {lifecycleLabel(b.status)}{' '}
          <span className="font-semibold text-foreground tabular-nums">
            {formatNumber(b.count)}
          </span>
        </span>
      ))}
    </div>
  )
}

export function PipelineCard({ summary }: PipelineCardProps) {
  const products = toMap(summary.productsByStatus)
  const max = Math.max(1, ...Object.values(products), 0)

  return (
    <Card className="h-full">
      <CardHeader className="border-b pb-5">
        <CardTitle>Жизненный цикл товаров</CardTitle>
        <CardDescription>
          {formatNumber(summary.totals.products)} товаров в системе
        </CardDescription>
      </CardHeader>

      <CardContent className="space-y-3">
        {PRODUCT_ORDER.map((status, i) => {
          const count = products[status] ?? 0
          const width = (count / max) * 100
          return (
            <div key={status} className="space-y-1">
              <div className="flex items-baseline justify-between gap-2 text-sm">
                <span className="text-foreground">{lifecycleLabel(status)}</span>
                <span className="text-muted-foreground tabular-nums">
                  {formatNumber(count)}
                </span>
              </div>
              <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full"
                  style={{
                    width: `${width}%`,
                    backgroundColor: `var(--chart-${Math.min(i + 1, 5)})`,
                  }}
                />
              </div>
            </div>
          )
        })}
      </CardContent>

      <CardFooter className="flex-col items-start gap-3 border-t pt-4">
        <div className="space-y-1.5">
          <span className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
            Заказы
          </span>
          <StatusPills buckets={summary.ordersByStatus} />
        </div>
        <div className="space-y-1.5">
          <span className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
            Отгрузки
          </span>
          <StatusPills buckets={summary.dispatchesByStatus} />
        </div>
      </CardFooter>
    </Card>
  )
}
