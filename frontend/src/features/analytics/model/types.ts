// Domain types for the analytics dashboard (camelCase). The wire shapes
// (snake_case) live in ../api/analyticsApi.ts and are mapped on the way in.

// The four on-chain event states + their canonical order now live in shared/lib
// (used by both analytics and traceability). Re-exported here so the analytics
// dashboard keeps importing them from its own model.
export type { ChainStatus } from '@/shared/lib'
export { CHAIN_STATUS_ORDER } from '@/shared/lib'

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
