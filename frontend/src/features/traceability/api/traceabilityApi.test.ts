import { describe, expect, it } from 'vitest'

import { mapTimeline, mapTxProof } from './traceabilityApi'

describe('mapTimeline', () => {
  it('lowercases chain_status and maps fields', () => {
    const tl = mapTimeline({
      product: { product_id: 'p1', qr_code: 'BOX-1', sku_name: 'SKU-1', status: 'SHIPPED' },
      steps: [
        {
          stage: 'receiving',
          event_type: 'wms.receiving.v1',
          occurred_at: '2026-06-03T10:00:00Z',
          event_id: 'e1',
          tx_hash: '0xabc',
          chain_status: 'COMMITTED',
          chain_updated_at: '2026-06-03T10:00:05Z',
          error_message: null,
        },
      ],
    })
    expect(tl.product.qrCode).toBe('BOX-1')
    expect(tl.steps[0].chainStatus).toBe('committed')
    expect(tl.steps[0].txHash).toBe('0xabc')
  })

  it('returns empty steps array when there are none', () => {
    const tl = mapTimeline({
      product: { product_id: 'p1', qr_code: 'BOX-1', sku_name: 'SKU-1', status: 'RECEIVED' },
      steps: [],
    })
    expect(tl.steps).toEqual([])
  })
})

describe('mapTxProof', () => {
  it('maps snake_case to camelCase', () => {
    const p = mapTxProof({
      found: true,
      status: 'success',
      block_number: 42,
      confirmations: 7,
      gas_used: 64000,
    })
    expect(p).toEqual({ found: true, status: 'success', blockNumber: 42, confirmations: 7, gasUsed: 64000 })
  })
})
