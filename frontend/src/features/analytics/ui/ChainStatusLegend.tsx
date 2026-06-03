import { cn } from '@/shared/lib'

import { chainStatusLabel } from '../model/format'
import { CHAIN_STATUS_ORDER } from '../model/types'
import { STATUS_COLOR } from './colors'

interface ChainStatusLegendProps {
  className?: string
}

// Shared legend for the four on-chain states, so the ring, stage bars, and any
// other chain visual read against one consistent key.
export function ChainStatusLegend({ className }: ChainStatusLegendProps) {
  return (
    <ul className={cn('flex flex-wrap gap-x-4 gap-y-1.5', className)}>
      {CHAIN_STATUS_ORDER.map((status) => (
        <li
          key={status}
          className="flex items-center gap-1.5 text-xs text-muted-foreground"
        >
          <span
            className="size-2 rounded-full"
            style={{ backgroundColor: STATUS_COLOR[status] }}
            aria-hidden="true"
          />
          {chainStatusLabel(status)}
        </li>
      ))}
    </ul>
  )
}
