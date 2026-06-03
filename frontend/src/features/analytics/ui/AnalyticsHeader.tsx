import { Button } from '@/shared/ui'

import { formatDateTime } from '../model/format'
import { RefreshIcon } from './icons'

interface AnalyticsHeaderProps {
  lastUpdatedMs: number | null
  isFetching: boolean
  onRefresh: () => void
}

export function AnalyticsHeader({
  lastUpdatedMs,
  isFetching,
  onRefresh,
}: AnalyticsHeaderProps) {
  const updatedLabel = lastUpdatedMs
    ? formatDateTime(new Date(lastUpdatedMs).toISOString())
    : '—'

  return (
    <header className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div className="space-y-1.5">
        <h1 className="font-heading text-3xl font-semibold tracking-tight text-foreground sm:text-4xl">
          Аналитика склада
        </h1>
        <p className="max-w-xl text-sm text-muted-foreground">
          Операционные метрики склада и подтверждение операций в блокчейне в
          реальном времени.
        </p>
      </div>

      <div className="flex items-center gap-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="relative flex size-2">
            <span className="absolute inline-flex h-full w-full rounded-full bg-status-committed/70 motion-safe:animate-ping" />
            <span className="relative inline-flex size-2 rounded-full bg-status-committed" />
          </span>
          <span>
            Обновлено{' '}
            <time className="font-medium text-foreground tabular-nums">
              {updatedLabel}
            </time>
          </span>
        </div>
        <Button
          variant="outline"
          size="sm"
          type="button"
          onClick={onRefresh}
          disabled={isFetching}
          aria-label="Обновить данные"
        >
          <RefreshIcon
            className={isFetching ? 'motion-safe:animate-spin' : undefined}
          />
          Обновить
        </Button>
      </div>
    </header>
  )
}
