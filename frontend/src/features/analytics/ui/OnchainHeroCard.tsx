import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/shared/ui'

import {
  chainStatusLabel,
  formatNumber,
  formatPercent,
  sharePercent,
} from '../model/format'
import { CHAIN_STATUS_ORDER, type ChainStatus } from '../model/types'
import type { OnchainReport } from '../model/types'
import { DonutRing, type RingSegment } from './charts/DonutRing'
import { STATUS_COLOR } from './colors'
import { CubeIcon } from './icons'

interface OnchainHeroCardProps {
  onchain: OnchainReport
}

// THE blockchain hero. A stacked confirmation ring with the headline rate at its
// centre, paired with a legend that breaks the four event states down by count
// and share. The denominator is stated explicitly so "confirmation rate" is
// never ambiguous.
export function OnchainHeroCard({ onchain }: OnchainHeroCardProps) {
  const counts: Record<ChainStatus, number> = {
    committed: onchain.committed,
    sent: onchain.sent,
    pending: onchain.pending,
    failed: onchain.failed,
  }

  const segments: RingSegment[] = CHAIN_STATUS_ORDER.map((status) => ({
    key: status,
    value: counts[status],
    color: STATUS_COLOR[status],
  }))

  return (
    <Card className="h-full">
      <CardHeader className="border-b pb-5">
        <CardTitle className="flex items-center gap-2 text-base">
          <CubeIcon className="size-4 text-muted-foreground" />
          Статус в блокчейне
        </CardTitle>
        <CardDescription>
          Подтверждение событий аудита в сети Avalanche
        </CardDescription>
      </CardHeader>

      <CardContent className="grid items-center gap-8 pt-2 sm:grid-cols-[auto_1fr]">
        <div className="flex flex-col items-center gap-3">
          <DonutRing segments={segments}>
            <span className="font-heading text-4xl font-semibold tracking-tight tabular-nums">
              {formatPercent(onchain.confirmationRate)}
            </span>
            <span className="mt-0.5 text-xs tracking-wide text-muted-foreground uppercase">
              подтверждено
            </span>
          </DonutRing>
          <p className="text-center text-xs text-muted-foreground">
            <span className="font-medium text-foreground tabular-nums">
              {formatNumber(onchain.committed)}
            </span>{' '}
            из{' '}
            <span className="font-medium text-foreground tabular-nums">
              {formatNumber(onchain.totalEvents)}
            </span>{' '}
            событий
          </p>
        </div>

        <ul className="space-y-1">
          {CHAIN_STATUS_ORDER.map((status) => (
            <li
              key={status}
              className="flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-muted/60"
            >
              <span
                className="size-2.5 shrink-0 rounded-full"
                style={{ backgroundColor: STATUS_COLOR[status] }}
                aria-hidden="true"
              />
              <span className="flex-1 text-sm text-foreground">
                {chainStatusLabel(status)}
              </span>
              <span className="text-sm font-semibold tabular-nums">
                {formatNumber(counts[status])}
              </span>
              <span className="w-12 text-right text-xs text-muted-foreground tabular-nums">
                {sharePercent(counts[status], onchain.totalEvents).toFixed(0)}%
              </span>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
