import type { ChainStatus } from '@/shared/lib'

export interface RecentProduct {
  productId: string
  qrCode: string
  skuName: string
  status: string
  updatedAt: string
}

export interface TimelineStep {
  stage: string // raw lowercase aggregate_type
  eventType: string
  occurredAt: string
  eventId: string
  txHash: string | null
  chainStatus: ChainStatus
  chainUpdatedAt: string | null
  errorMessage: string | null
}

export interface ProductHeader {
  productId: string
  qrCode: string
  skuName: string
  status: string
}

export interface ProductTimeline {
  product: ProductHeader
  steps: TimelineStep[]
}

export type TxProofStatus = 'success' | 'failed' | 'pending'

export interface TxProof {
  found: boolean
  status: TxProofStatus
  blockNumber: number
  confirmations: number
  gasUsed: number
}
