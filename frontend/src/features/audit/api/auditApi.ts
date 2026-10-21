// Audit endpoint client: reads the DB record of one product for the auditor.
import { apiClient } from '@/shared/api'

import type { DbAction, DbDetails, ProductAudit } from '../model/types'
import type { Hex } from 'viem'

interface ApiEnvelope<T> {
  success: boolean
  data: T | null
  error: { code: string; message: string } | null
}

interface DetailsResponse {
  cargoplaceId?: string
  storageBinId?: string
  shippingBufferBinId?: string
  dispatchId?: string
  orderIds?: string[]
  operatorId: string
  occurredAtMs: number
}

interface ActionResponse {
  actionId: string
  operationId: string
  partIndex: number
  actionType: number
  itemIds: string[]
  salt: string
  submittedAt: string
  operatorId: string
  operator: string
  keyOwnerId: string
  orgId: string
  orgName: string
  keyId: string
  chainStatus: string
  rejectReason: number | null
  details: DetailsResponse | null
  detailsError: string | null
}

interface ProductAuditResponse {
  productId: string
  itemId: string
  actions: ActionResponse[]
}

function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }
  return envelope.data
}

export function mapProductAudit(r: ProductAuditResponse): ProductAudit {
  return {
    productId: r.productId,
    itemId: r.itemId as Hex,
    actions: r.actions.map(
      (a): DbAction => ({
        ...a,
        salt: a.salt as Hex,
        keyId: a.keyId as Hex,
        details: a.details ? ({ ...a.details } as DbDetails) : null,
      }),
    ),
  }
}

export async function getProductAudit(key: string): Promise<ProductAudit> {
  const { data } = await apiClient.get<ApiEnvelope<ProductAuditResponse>>(
    `/audit/products/${encodeURIComponent(key)}`,
  )
  return mapProductAudit(unwrap(data))
}
