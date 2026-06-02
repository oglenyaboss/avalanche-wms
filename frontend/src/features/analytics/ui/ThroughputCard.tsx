import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/shared/ui'
import { cn } from '@/shared/lib'

import { formatDayShort, formatNumber, stageLabel } from '../model/format'
import type { ThroughputRange } from '../model/useAnalytics'
import type { ThroughputReport } from '../model/types'
import { AreaChart, type AreaSeries } from './charts/AreaChart'
import { stageColor } from './colors'

interface ThroughputCardProps {
  throughput: ThroughputReport
  days: ThroughputRange
  onDaysChange: (days: ThroughputRange) => void
}

const RANGES: ThroughputRange[] = [7, 14, 30]

export function ThroughputCard({
  throughput,
  days,
  onDaysChange,
}: ThroughputCardProps) {
  const areaSeries: AreaSeries[] = throughput.series.map((s, i) => ({
    key: s.aggregateType,
    color: stageColor(s.aggregateType, i),
    counts: s.counts,
  }))

  const total = throughput.totals.reduce((acc, v) => acc + v, 0)
  const peak = throughput.totals.length ? Math.max(...throughput.totals) : 0

  // A sparse, evenly-spaced subset of day labels to avoid a crowded axis.
  const step = Math.max(1, Math.ceil(throughput.days.length / 6))
  const axisLabels = throughput.days.filter((_, i) => i % step === 0)

  return (
    <Card>
      <CardHeader className="border-b pb-5">
        <CardTitle>Поток событий аудита</CardTitle>
        <CardDescription>
          Объём событий по этапам · всего{' '}
          <span className="font-medium text-foreground tabular-nums">
            {formatNumber(total)}
          </span>{' '}
          · пик{' '}
          <span className="font-medium text-foreground tabular-nums">
            {formatNumber(peak)}
          </span>
          /день
        </CardDescription>
        <CardAction>
          <div className="inline-flex rounded-lg bg-muted p-0.5">
            {RANGES.map((r) => (
              <button
                key={r}
                type="button"
                onClick={() => onDaysChange(r)}
                aria-pressed={days === r}
                className={cn(
                  'rounded-md px-2.5 py-1 text-xs font-medium tabular-nums transition-colors',
                  days === r
                    ? 'bg-card text-foreground ring-1 ring-foreground/10'
                    : 'text-muted-foreground hover:text-foreground',
                )}
              >
                {r} дн
              </button>
            ))}
          </div>
        </CardAction>
      </CardHeader>

      <CardContent className="space-y-3">
        <AreaChart series={areaSeries} height={220} />

        <div className="flex justify-between text-[11px] text-muted-foreground tabular-nums">
          {axisLabels.map((day) => (
            <span key={day}>{formatDayShort(day)}</span>
          ))}
        </div>

        <ul className="flex flex-wrap gap-x-4 gap-y-1.5 border-t pt-3">
          {throughput.series.map((s, i) => (
            <li
              key={s.aggregateType}
              className="flex items-center gap-1.5 text-xs text-muted-foreground"
            >
              <span
                className="size-2 rounded-full"
                style={{ backgroundColor: stageColor(s.aggregateType, i) }}
                aria-hidden="true"
              />
              {stageLabel(s.aggregateType)}
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
