import type { ChainStatus } from '../model/types'

// Semantic accent — used ONLY for on-chain event state. These resolve to the
// --status-* CSS custom properties (light/dark aware) defined in app/index.css.
export const STATUS_COLOR: Record<ChainStatus, string> = {
  committed: 'var(--status-committed)',
  sent: 'var(--status-sent)',
  pending: 'var(--status-pending)',
  failed: 'var(--status-failed)',
}

// Non-semantic volume/stage series use the monochrome --chart-* ramp so colour
// stays reserved for chain status alone.
const STAGE_COLOR: Record<string, string> = {
  receiving: 'var(--chart-2)',
  putaway: 'var(--chart-3)',
  picking: 'var(--chart-4)',
  shipping: 'var(--chart-5)',
}

export function stageColor(aggregateType: string, index: number): string {
  return STAGE_COLOR[aggregateType] ?? `var(--chart-${(index % 5) + 1})`
}
