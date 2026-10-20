// getProductAudit: envelope unwrapping, URL encoding and camelCase mapping.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AxiosResponse } from 'axios'

import { apiClient } from '@/shared/api'

import { getProductAudit, mapProductAudit } from './auditApi'

vi.mock('@/shared/api', () => ({ apiClient: { get: vi.fn() } }))

const response = {
  productId: '01929c5e-6f00-7000-8000-000000000103',
  itemId: '0x4ac4a515f579f79cce4ba71ea357787f5208aa5ca7c4f612201be0dd2673ee18',
  actions: [
    {
      actionId: '01929c5e-6f00-7000-8000-000000001003',
      operationId: '01929c5e-6f00-7000-8000-000000000777',
      partIndex: 1,
      actionType: 2,
      itemIds: ['01929c5e-6f00-7000-8000-000000000101'],
      salt: '0x' + '08'.repeat(32),
      submittedAt: '2025-10-18T10:01:00Z',
      operatorId: '01929c5e-6f00-7000-8000-0000000000b1',
      operator: 'Оператор',
      orgId: '01929c5e-6f00-7000-8000-0000000000a1',
      orgName: 'Склад №1',
      keyId: '0x' + '59'.repeat(32),
      chainStatus: 'COMMITTED',
      rejectReason: null,
      details: {
        storageBinId: '01929c5e-6f00-7000-8000-0000000000d1',
        operatorId: '01929c5e-6f00-7000-8000-0000000000b1',
        occurredAtMs: 1760781660000,
      },
      detailsError: null,
    },
  ],
}

function mockGet(envelope: unknown) {
  vi.mocked(apiClient.get).mockResolvedValue({ data: envelope } as AxiosResponse)
}

beforeEach(() => {
  vi.mocked(apiClient.get).mockReset()
})

describe('mapProductAudit', () => {
  it('maps the backend JSON to the audit types', () => {
    const audit = mapProductAudit(response)
    expect(audit.productId).toBe(response.productId)
    expect(audit.itemId).toBe(response.itemId)
    expect(audit.actions[0].salt).toBe(response.actions[0].salt)
    expect(audit.actions[0].details?.storageBinId).toBe('01929c5e-6f00-7000-8000-0000000000d1')
    expect(audit.actions[0].detailsError).toBeNull()
  })
})

describe('getProductAudit', () => {
  it('requests the product key and unwraps the envelope', async () => {
    mockGet({ success: true, data: response, error: null })
    const audit = await getProductAudit('BOX-1')
    expect(apiClient.get).toHaveBeenCalledWith('/audit/products/BOX-1')
    expect(audit.actions).toHaveLength(1)
    expect(audit.actions[0].actionId).toBe('01929c5e-6f00-7000-8000-000000001003')
  })

  it('encodes the product key into the URL', async () => {
    mockGet({ success: true, data: response, error: null })
    await getProductAudit('a b/1')
    expect(apiClient.get).toHaveBeenCalledWith('/audit/products/a%20b%2F1')
  })

  it('throws the backend error message', async () => {
    mockGet({ success: false, data: null, error: { code: 'PRODUCT_NOT_FOUND', message: 'Товар не найден' } })
    await expect(getProductAudit('x')).rejects.toThrow('Товар не найден')
  })
})
