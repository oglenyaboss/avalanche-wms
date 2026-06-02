import { cn } from '@/shared/lib'

export interface BarSegment {
  key: string
  value: number
  color: string
}

interface StackedBarProps {
  segments: BarSegment[]
  /** Denominator. Defaults to the sum of segment values; pass a larger total to
   *  leave a track remainder (e.g. events not yet on-chain). */
  total?: number
  className?: string
}

// A thin horizontal stacked bar. The muted track shows through for the unfilled
// remainder, so "pending" reads as the gap rather than needing its own segment.
export function StackedBar({ segments, total, className }: StackedBarProps) {
  const sum = total ?? segments.reduce((acc, s) => acc + s.value, 0)
  return (
    <div
      className={cn(
        'flex h-2.5 w-full gap-px overflow-hidden rounded-full bg-muted',
        className,
      )}
    >
      {sum > 0 &&
        segments.map((s) => {
          const pct = (s.value / sum) * 100
          if (pct <= 0) return null
          return (
            <div
              key={s.key}
              className="h-full first:rounded-l-full last:rounded-r-full"
              style={{ width: `${pct}%`, backgroundColor: s.color }}
            />
          )
        })}
    </div>
  )
}
