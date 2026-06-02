// Domain types for the analytics dashboard (camelCase). The wire shapes
// (snake_case) live in ../api/analyticsApi.ts and are mapped on the way in.

// The four on-chain event states, lowercased for use as map keys / class
// suffixes. Mirrors the backend onchain_event_status enum.
export type ChainStatus = 'committed' | 'sent' | 'pending' | 'failed'

export const CHAIN_STATUS_ORDER: ChainStatus[] = [
  'committed',
  'sent',
  'pending',
  'failed',
]

export interface Totals {
  skus: number
  products: number
  orders: number
  dispatches: number
  destinations: number
}

export interface StatusBucket {
  status: string
  count: number
}

export interface SummaryReport {
  totals: Totals
  eventsToday: number
  productsByStatus: StatusBucket[]
  ordersByStatus: StatusBucket[]
  dispatchesByStatus: StatusBucket[]
}

export interface OnchainStageBreakdown {
  aggregateType: string
  total: number
  committed: number
  sent: number
  pending: number
  failed: number
}

export interface OnchainEvent {
  eventId: string
  aggregateType: string
  txHash: string | null
  errorMessage: string | null
  updatedAt: string
}

export interface OnchainReport {
  totalEvents: number
  committed: number
  sent: number
  pending: number
  failed: number
  /** COMMITTED / totalEvents, in the range 0..1. */
  confirmationRate: number
  byStage: OnchainStageBreakdown[]
  recentFailed: OnchainEvent[]
  recentCommitted: OnchainEvent[]
}

export interface ThroughputSeries {
  aggregateType: string
  counts: number[]
}

export interface ThroughputReport {
  days: string[]
  series: ThroughputSeries[]
  totals: number[]
}
