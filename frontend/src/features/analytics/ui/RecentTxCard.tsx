import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/shared/ui'

import { formatDateTime, stageLabel, truncateHash } from '../model/format'
import type { OnchainEvent } from '../model/types'
import { ArrowUpRightIcon, CubeIcon } from './icons'

interface RecentTxCardProps {
  events: OnchainEvent[]
}

// Live feed of the most recent committed transactions. Tx hashes are shown
// monospace and middle-truncated; the full hash is available on hover.
export function RecentTxCard({ events }: RecentTxCardProps) {
  return (
    <Card className="h-full">
      <CardHeader className="border-b pb-5">
        <CardTitle className="flex items-center gap-2">
          <CubeIcon className="size-4 text-status-committed" />
          Последние транзакции
        </CardTitle>
        <CardDescription>Подтверждённые записи в блокчейне</CardDescription>
      </CardHeader>

      <CardContent>
        {events.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-8 text-center">
            <span className="flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
              <CubeIcon className="size-5" />
            </span>
            <p className="text-sm text-muted-foreground">
              Пока нет подтверждённых транзакций.
            </p>
          </div>
        ) : (
          <ul className="divide-y divide-border">
            {events.map((e) => (
              <li key={e.eventId} className="flex items-center gap-3 py-3 first:pt-0">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-status-committed/12 text-status-committed">
                  <ArrowUpRightIcon className="size-4" />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-sm font-medium text-foreground">
                      {stageLabel(e.aggregateType)}
                    </span>
                    <time className="shrink-0 text-xs text-muted-foreground tabular-nums">
                      {formatDateTime(e.updatedAt)}
                    </time>
                  </div>
                  <p
                    className="truncate font-mono text-xs text-muted-foreground"
                    title={e.txHash ?? undefined}
                  >
                    {truncateHash(e.txHash, 10, 8)}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
