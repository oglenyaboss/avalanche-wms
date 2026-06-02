import { Button, Card, CardContent } from '@/shared/ui'
import { cn } from '@/shared/lib'

import { AlertTriangleIcon } from './icons'
import { Skeleton } from './Skeleton'

interface CardSkeletonProps {
  className?: string
  lines?: number
}

// Loading placeholder shaped like a card: a title line plus a few content rows.
export function CardSkeleton({ className, lines = 4 }: CardSkeletonProps) {
  return (
    <Card className={className}>
      <CardContent className="space-y-3 py-1">
        <Skeleton className="h-4 w-2/5" />
        {Array.from({ length: lines }).map((_, i) => (
          <Skeleton key={i} className="h-3 w-full" />
        ))}
      </CardContent>
    </Card>
  )
}

interface CardErrorProps {
  message: string
  onRetry: () => void
  className?: string
}

export function CardError({ message, onRetry, className }: CardErrorProps) {
  return (
    <Card className={className}>
      <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
        <span className="flex size-10 items-center justify-center rounded-full bg-status-failed/12 text-status-failed">
          <AlertTriangleIcon className="size-5" />
        </span>
        <p className="max-w-sm text-sm text-muted-foreground">{message}</p>
        <Button variant="outline" size="sm" type="button" onClick={onRetry}>
          Повторить
        </Button>
      </CardContent>
    </Card>
  )
}

interface KpiStripSkeletonProps {
  className?: string
}

export function KpiStripSkeleton({ className }: KpiStripSkeletonProps) {
  return (
    <div className={cn('grid grid-cols-2 gap-4 lg:grid-cols-4', className)}>
      {Array.from({ length: 4 }).map((_, i) => (
        <div
          key={i}
          className="space-y-3 rounded-xl bg-card px-5 py-4 ring-1 ring-foreground/10"
        >
          <Skeleton className="h-3 w-1/2" />
          <Skeleton className="h-8 w-2/3" />
          <Skeleton className="h-3 w-3/4" />
        </div>
      ))}
    </div>
  )
}
