import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/shared/ui'

import { formatNumber, sharePercent, stageLabel } from '../model/format'
import type { OnchainStageBreakdown } from '../model/types'
import { ChainStatusLegend } from './ChainStatusLegend'
import { StackedBar, type BarSegment } from './charts/StackedBar'
import { STATUS_COLOR } from './colors'

interface StageBreakdownCardProps {
  stages: OnchainStageBreakdown[]
}

// Per-stage onchain confirmation. Each bar is filled by the four states across
// the stage's own total, so an operator can see at a glance which stage is
// lagging behind on-chain.
export function StageBreakdownCard({ stages }: StageBreakdownCardProps) {
  return (
    <Card className="h-full">
      <CardHeader className="border-b pb-5">
        <CardTitle>Подтверждение по этапам</CardTitle>
        <CardDescription>
          Доля событий каждого этапа, записанных в блокчейн
        </CardDescription>
      </CardHeader>

      <CardContent className="space-y-5">
        {stages.map((st) => {
          const segments: BarSegment[] = [
            { key: 'committed', value: st.committed, color: STATUS_COLOR.committed },
            { key: 'sent', value: st.sent, color: STATUS_COLOR.sent },
            { key: 'pending', value: st.pending, color: STATUS_COLOR.pending },
            { key: 'failed', value: st.failed, color: STATUS_COLOR.failed },
          ]
          return (
            <div key={st.aggregateType} className="space-y-2">
              <div className="flex items-baseline justify-between gap-2">
                <span className="text-sm font-medium text-foreground">
                  {stageLabel(st.aggregateType)}
                </span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatNumber(st.committed)}/{formatNumber(st.total)} ·{' '}
                  {sharePercent(st.committed, st.total).toFixed(0)}%
                </span>
              </div>
              <StackedBar segments={segments} total={st.total} />
            </div>
          )
        })}
      </CardContent>

      <CardFooter className="border-t pt-4">
        <ChainStatusLegend />
      </CardFooter>
    </Card>
  )
}
