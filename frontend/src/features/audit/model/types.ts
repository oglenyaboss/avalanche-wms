// Types of the product audit: backend JSON shapes and the per-stage audit result.
import type { Hex } from 'viem'

import type { ActionDetails } from '@/entities/signed-action'

export type ProductAudit = {
  productId: string
  itemId: Hex
  actions: DbAction[]
}

export type DbAction = {
  actionId: string
  operationId: string
  partIndex: number
  actionType: number
  itemIds: string[]
  salt: Hex
  submittedAt: string
  operatorId: string
  operator: string
  keyOwnerId: string
  orgId: string
  orgName: string
  keyId: Hex
  chainStatus: string
  rejectReason: number | null
  details: DbDetails | null
  detailsError: string | null
}

export type DbDetails = Omit<ActionDetails, 'salt'>

export type AuditCheck = 'ok' | 'mismatch' | 'skipped'

export type AuditStatus = 'applied' | 'rejected' | 'pending' | 'missingOnChain' | 'missingInDb'

export type AuditStage = {
  actionId: string
  actionType: number
  status: AuditStatus
  operator: string | null
  operatorId: string | null
  orgId: string | null
  orgName: string | null
  blockNumber: bigint | null
  blockTime: Date | null
  txHash: Hex | null
  rejectReason: number | null
  detailsHash: AuditCheck
  items: AuditCheck
  signer: AuditCheck
  signature: AuditCheck
  problems: string[]
}
