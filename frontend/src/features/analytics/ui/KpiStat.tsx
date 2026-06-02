import type { ReactNode } from 'react'

import { cn } from '@/shared/lib'

interface KpiStatProps {
  label: string
  value: string
  hint?: ReactNode
  /** CSS colour (e.g. a --status-* var) for the leading accent rule + dot. */
  accent?: string
  icon?: ReactNode
}

// A single headline metric tile. A thin accent rule on the left encodes the
// metric's semantic colour when relevant; numbers use tabular figures so the
// strip stays aligned as values change.
export function KpiStat({ label, value, hint, accent, icon }: KpiStatProps) {
  return (
    <div className="relative overflow-hidden rounded-xl bg-card px-5 py-4 ring-1 ring-foreground/10">
      {accent && (
        <span
          className="absolute inset-y-0 left-0 w-1"
          style={{ backgroundColor: accent }}
          aria-hidden="true"
        />
      )}
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {label}
        </span>
        {icon && <span className="text-muted-foreground">{icon}</span>}
      </div>
      <div
        className={cn(
          'mt-2 font-heading text-3xl font-semibold tracking-tight tabular-nums',
        )}
      >
        {value}
      </div>
      {hint && (
        <div className="mt-1 text-xs text-muted-foreground">{hint}</div>
      )}
    </div>
  )
}
