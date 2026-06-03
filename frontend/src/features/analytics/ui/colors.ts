// On-chain status colours now live in shared/lib (used by both analytics and
// traceability). Re-exported here so analytics components keep their import path.
export { STATUS_COLOR } from '@/shared/lib'

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
