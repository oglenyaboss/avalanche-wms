import { apiClient } from '@/shared/api'
import type { ChainStatus } from '@/shared/lib'

import type {
  ProductTimeline,
  RecentProduct,
  TimelineStep,
  TxProof,
} from '../model/types'

interface ApiEnvelope<T> {
  success: boolean
  data: T | null
  error: { code: string; message: string } | null
}

interface RecentProductResponse {
  product_id: string
  qr_code: string
  sku_name: string
  status: string
  updated_at: string
}

interface TimelineStepResponse {
  stage: string
  event_type: string
  occurred_at: string
  event_id: string
  tx_hash: string | null
  chain_status: string
  chain_updated_at: string | null
  error_message: string | null
}

interface TimelineResponse {
  product: { product_id: string; qr_code: string; sku_name: string; status: string }
  steps: TimelineStepResponse[]
}

interface TxProofResponse {
  found: boolean
  status: string
  block_number?: number
  confirmations?: number
  gas_used?: number
}

function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }
  return envelope.data
}

function mapStep(s: TimelineStepResponse): TimelineStep {
  return {
    stage: s.stage,
    eventType: s.event_type,
    occurredAt: s.occurred_at,
    eventId: s.event_id,
    txHash: s.tx_hash,
    chainStatus: s.chain_status.toLowerCase() as ChainStatus,
    chainUpdatedAt: s.chain_updated_at,
    errorMessage: s.error_message,
  }
}

export function mapTimeline(r: TimelineResponse): ProductTimeline {
  return {
    product: {
      productId: r.product.product_id,
      qrCode: r.product.qr_code,
      skuName: r.product.sku_name,
      status: r.product.status,
    },
    steps: r.steps.map(mapStep),
  }
}

export function mapTxProof(r: TxProofResponse): TxProof {
  return {
    found: r.found,
    status: r.status as TxProof['status'],
    blockNumber: r.block_number ?? 0,
    confirmations: r.confirmations ?? 0,
    gasUsed: r.gas_used ?? 0,
  }
}

export async function getRecentProducts(limit = 20): Promise<RecentProduct[]> {
  const { data } = await apiClient.get<ApiEnvelope<RecentProductResponse[]>>(
    '/products/recent',
    { params: { limit } }
  )
  return unwrap(data).map((p) => ({
    productId: p.product_id,
    qrCode: p.qr_code,
    skuName: p.sku_name,
    status: p.status,
    updatedAt: p.updated_at,
  }))
}

export async function getProductTimeline(key: string): Promise<ProductTimeline> {
  const { data } = await apiClient.get<ApiEnvelope<TimelineResponse>>(
    '/products/timeline',
    { params: { key } }
  )
  return mapTimeline(unwrap(data))
}

export async function getTxProof(hash: string): Promise<TxProof> {
  const { data } = await apiClient.get<ApiEnvelope<TxProofResponse>>(
    `/onchain/tx/${hash}`
  )
  return mapTxProof(unwrap(data))
}
