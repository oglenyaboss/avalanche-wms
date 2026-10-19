import { describe, expect, it, vi } from 'vitest'

const apiMocks = vi.hoisted(() => ({
  apiClient: { post: vi.fn() },
}))

vi.mock('@/shared/api', () => apiMocks)

import {
  draftUnloadToShippingBuffer,
  submitUnloadToShippingBuffer,
} from './assemblyApi'

describe('draftUnloadToShippingBuffer', () => {
  it('POSTs the buffer bin to the draft endpoint, maps the draft', async () => {
    apiMocks.apiClient.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          operation_id: '01929c5e-op-1',
          action_type: 4,
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
    })

    expect(await draftUnloadToShippingBuffer('01929c5e-buf-1')).toEqual({
      operationId: '01929c5e-op-1',
      actionType: 4,
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
    expect(apiMocks.apiClient.post).toHaveBeenCalledWith(
      '/assembly/scan-shipping-buffer/draft',
      { buffer_bin_id: '01929c5e-buf-1' }
    )
  })
})

describe('submitUnloadToShippingBuffer', () => {
  it('POSTs the operation id and snake_case assertions, maps the unload result', async () => {
    apiMocks.apiClient.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          buffer_bin_id: '01929c5e-buf-1',
          products_placed: 2,
          orders_assembled: 1,
          operation_id: '01929c5e-op-1',
        },
        error: null,
      },
    })

    expect(
      await submitUnloadToShippingBuffer('01929c5e-op-1', [
        {
          actionId: '01929c5e-a-1',
          credentialId: 'AQ',
          authenticatorData: 'Ag',
          clientDataJson: 'Aw',
          signature: 'BA',
        },
      ])
    ).toEqual({
      bufferBinId: '01929c5e-buf-1',
      productsPlaced: 2,
      ordersAssembled: 1,
      operationId: '01929c5e-op-1',
    })
    expect(apiMocks.apiClient.post).toHaveBeenCalledWith(
      '/assembly/scan-shipping-buffer/submit',
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
      }
    )
  })
})
