import { describe, expect, it, vi } from 'vitest'

const apiMocks = vi.hoisted(() => ({
  apiClient: { post: vi.fn() },
}))

vi.mock('@/shared/api', () => apiMocks)

import { draftShip, submitShip } from './shippingApi'

const DRAFT_ENVELOPE = {
  data: {
    success: true,
    data: {
      operation_id: '01929c5e-op-1',
      action_type: 6,
      signer_key_id: '0xabc',
      rp_id: 'localhost',
      allow_credentials: [{ type: 'public-key', id: 'AQID' }],
      user_verification: 'required',
      timeout: 60000,
      parts: [
        {
          action_id: '01929c5e-a-1',
          item_ids: ['01929c5e-p-1', '01929c5e-p-2'],
          details_hash: '0xdeadbeef',
          challenge: 'AQID',
        },
      ],
    },
    error: null,
  },
}

describe('draftShip', () => {
  it('POSTs the buffer and dispatch to the draft endpoint in bulk mode, maps the draft', async () => {
    apiMocks.apiClient.post.mockResolvedValue(DRAFT_ENVELOPE)

    expect(
      await draftShip({
        bufferBinId: '01929c5e-buf-1',
        dispatchId: '01929c5e-dsp-1',
      }),
    ).toEqual({
      operationId: '01929c5e-op-1',
      actionType: 6,
      signerKeyId: '0xabc',
      rpId: 'localhost',
      allowCredentials: [{ type: 'public-key', id: 'AQID' }],
      userVerification: 'required',
      timeout: 60000,
      parts: [
        {
          actionId: '01929c5e-a-1',
          itemIds: ['01929c5e-p-1', '01929c5e-p-2'],
          detailsHash: '0xdeadbeef',
          challenge: 'AQID',
        },
      ],
    })
    // Bulk mode omits product_ids entirely.
    expect(apiMocks.apiClient.post).toHaveBeenCalledWith(
      '/shipping/ship/draft',
      {
        buffer_bin_id: '01929c5e-buf-1',
        dispatch_id: '01929c5e-dsp-1',
      },
    )
  })

  it('sends product_ids in spot mode', async () => {
    apiMocks.apiClient.post.mockResolvedValue(DRAFT_ENVELOPE)

    await draftShip({
      bufferBinId: '01929c5e-buf-1',
      dispatchId: '01929c5e-dsp-1',
      productIds: ['01929c5e-p-2'],
    })

    expect(apiMocks.apiClient.post).toHaveBeenCalledWith('/shipping/ship/draft', {
      buffer_bin_id: '01929c5e-buf-1',
      dispatch_id: '01929c5e-dsp-1',
      product_ids: ['01929c5e-p-2'],
    })
  })
})

describe('submitShip', () => {
  it('POSTs the operation id and snake_case assertions, maps the ship result', async () => {
    apiMocks.apiClient.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          products_shipped: 2,
          outbox_events_created: 2,
          orders_completed: 1,
          orders_partially_shipped: 0,
          dispatch_departed: true,
          buffer_remaining: 0,
          operation_id: '01929c5e-op-1',
        },
        error: null,
      },
    })

    expect(
      await submitShip('01929c5e-op-1', [
        {
          actionId: '01929c5e-a-1',
          credentialId: 'AQ',
          authenticatorData: 'Ag',
          clientDataJson: 'Aw',
          signature: 'BA',
        },
      ]),
    ).toEqual({
      productsShipped: 2,
      outboxEventsCreated: 2,
      ordersCompleted: 1,
      ordersPartiallyShipped: 0,
      dispatchDeparted: true,
      bufferRemaining: 0,
      operationId: '01929c5e-op-1',
    })
    expect(apiMocks.apiClient.post).toHaveBeenCalledWith(
      '/shipping/ship/submit',
      {
        operation_id: '01929c5e-op-1',
        assertions: [
          {
            action_id: '01929c5e-a-1',
            credential_id: 'AQ',
            authenticator_data: 'Ag',
            client_data_json: 'Aw',
            signature: 'BA',
          },
        ],
      },
    )
  })
})
