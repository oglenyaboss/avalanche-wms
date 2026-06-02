import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/shared/ui'

import { formatDateTime, stageLabel, truncateHash } from '../model/format'
import type { OnchainEvent } from '../model/types'
import { AlertTriangleIcon, CheckIcon } from './icons'

interface FailedEventsCardProps {
  events: OnchainEvent[]
}

// Attention list: events the chain rejected. Empty is the good state, so it gets
// an affirmative message rather than a blank panel.
export function FailedEventsCard({ events }: FailedEventsCardProps) {
  return (
    <Card className="h-full">
      <CardHeader className="border-b pb-5">
        <CardTitle className="flex items-center gap-2">
          <AlertTriangleIcon className="size-4 text-status-failed" />
          Отклонённые события
        </CardTitle>
        <CardDescription>Транзакции, отклонённые блокчейном</CardDescription>
      </CardHeader>

      <CardContent>
        {events.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-8 text-center">
            <span className="flex size-10 items-center justify-center rounded-full bg-status-committed/12 text-status-committed">
              <CheckIcon className="size-5" />
            </span>
            <p className="text-sm text-muted-foreground">
              Нет отклонённых событий — аудит синхронизирован.
            </p>
          </div>
        ) : (
          <ul className="divide-y divide-border">
            {events.map((e) => (
              <li key={e.eventId} className="flex items-start gap-3 py-3 first:pt-0">
                <span
                  className="mt-1 size-2 shrink-0 rounded-full bg-status-failed"
                  aria-hidden="true"
                />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-sm font-medium text-foreground">
                      {stageLabel(e.aggregateType)}
                    </span>
                    <time className="shrink-0 text-xs text-muted-foreground tabular-nums">
                      {formatDateTime(e.updatedAt)}
                    </time>
                  </div>
                  <p className="truncate text-xs text-status-failed" title={e.errorMessage ?? undefined}>
                    {e.errorMessage ?? 'Неизвестная ошибка'}
                  </p>
                  <p className="font-mono text-[11px] text-muted-foreground">
                    {truncateHash(e.eventId, 8, 6)}
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
