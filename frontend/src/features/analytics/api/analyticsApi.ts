import { apiClient } from '@/shared/api'

import type {
  OnchainEvent,
  OnchainReport,
  OnchainStageBreakdown,
  StatusBucket,
  SummaryReport,
  ThroughputReport,
  ThroughputSeries,
  Totals,
} from '../model/types'

// ── Wire shapes (snake_case, as returned by the WMS API) ────────────────────

interface ApiEnvelope<T> {
  success: boolean
  data: T | null
  error: { code: string; message: string } | null
}

// A 200 with success:false / null data would crash the mappers on a null
// dereference; guard against that contract violation (see assemblyApi).
function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }
  return envelope.data
}

interface StatusBucketResponse {
  status: string
  count: number
}

interface SummaryResponse {
  totals: Totals
  events_today: number
  products_by_status: StatusBucketResponse[]
  orders_by_status: StatusBucketResponse[]
  dispatches_by_status: StatusBucketResponse[]
}

interface StageResponse {
  aggregate_type: string
  total: number
  committed: number
  sent: number
  pending: number
  failed: number
}

interface OnchainEventResponse {
  event_id: string
  aggregate_type: string
  tx_hash?: string | null
  error_message?: string | null
  updated_at: string
}

interface OnchainResponse {
  total_events: number
  committed: number
  sent: number
  pending: number
  failed: number
  confirmation_rate: number
  by_stage: StageResponse[]
  recent_failed: OnchainEventResponse[]
  recent_committed: OnchainEventResponse[]
}

interface ThroughputSeriesResponse {
  aggregate_type: string
  counts: number[]
}

interface ThroughputResponse {
  days: string[]
  series: ThroughputSeriesResponse[]
  totals: number[]
}

// ── Mappers ─────────────────────────────────────────────────────────────────

function mapBuckets(items: StatusBucketResponse[]): StatusBucket[] {
  return items.map((b) => ({ status: b.status, count: b.count }))
}

function mapStage(s: StageResponse): OnchainStageBreakdown {
  return {
    aggregateType: s.aggregate_type,
    total: s.total,
    committed: s.committed,
    sent: s.sent,
    pending: s.pending,
    failed: s.failed,
  }
}

function mapEvent(e: OnchainEventResponse): OnchainEvent {
  return {
    eventId: e.event_id,
    aggregateType: e.aggregate_type,
    txHash: e.tx_hash ?? null,
    errorMessage: e.error_message ?? null,
    updatedAt: e.updated_at,
  }
}

function mapSeries(s: ThroughputSeriesResponse): ThroughputSeries {
  return { aggregateType: s.aggregate_type, counts: s.counts }
}

// ── Requests ─────────────────────────────────────────────────────────────────

export async function getSummary(): Promise<SummaryReport> {
  const { data } =
    await apiClient.get<ApiEnvelope<SummaryResponse>>('/analytics/summary')
  const result = unwrap(data)
  return {
    totals: result.totals,
    eventsToday: result.events_today,
    productsByStatus: mapBuckets(result.products_by_status),
    ordersByStatus: mapBuckets(result.orders_by_status),
    dispatchesByStatus: mapBuckets(result.dispatches_by_status),
  }
}

export async function getOnchain(): Promise<OnchainReport> {
  const { data } =
    await apiClient.get<ApiEnvelope<OnchainResponse>>('/analytics/onchain')
  const result = unwrap(data)
  return {
    totalEvents: result.total_events,
    committed: result.committed,
    sent: result.sent,
    pending: result.pending,
    failed: result.failed,
    confirmationRate: result.confirmation_rate,
    byStage: result.by_stage.map(mapStage),
    recentFailed: result.recent_failed.map(mapEvent),
    recentCommitted: result.recent_committed.map(mapEvent),
  }
}

export async function getThroughput(days: number): Promise<ThroughputReport> {
  const { data } = await apiClient.get<ApiEnvelope<ThroughputResponse>>(
    `/analytics/throughput?days=${encodeURIComponent(days)}`,
  )
  const result = unwrap(data)
  return {
    days: result.days,
    series: result.series.map(mapSeries),
    totals: result.totals,
  }
}
