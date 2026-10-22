import { describe, expect, it, vi } from 'vitest'

const apiMocks = vi.hoisted(() => ({
  apiClient: { post: vi.fn() },
}))

vi.mock('@/shared/api', () => apiMocks)

import { draftCloseCargoplace, submitCloseCargoplace } from './receivingTableApi'

describe('draftCloseCargoplace', () => {
  it('POSTs the cargoplace id to the draft endpoint and returns the mapped draft', async () => {
    apiMocks.apiClient.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          operation_id: '01929c5e-op-1',
          action_type: 3,
          signer_key_id: '0xabc',
          rp_id: 'localhost',
          allow_credentials: [{ type: 'public-key', id: 'AQID' }],
          user_verification: 'required',
          timeout: 60000,
          parts: [
            {
              action_id: '01929c5e-a-1',
              item_ids: ['01929c5e-i-1'],
              details_hash: '0xdeadbeef',
              challenge: 'AQID',
            },
          ],
        },
        error: null,
      },
    })

    expect(await draftCloseCargoplace('01929c5e-cp-1')).toEqual({
      operationId: '01929c5e-op-1',
      actionType: 3,
      signerKeyId: '0xabc',
      rpId: 'localhost',
      allowCredentials: [{ type: 'public-key', id: 'AQID' }],
      userVerification: 'required',
      timeout: 60000,
      parts: [
        {
          actionId: '01929c5e-a-1',
          itemIds: ['01929c5e-i-1'],
          detailsHash: '0xdeadbeef',
          challenge: 'AQID',
        },
      ],
    })
    expect(apiMocks.apiClient.post).toHaveBeenCalledWith(
      '/receiving/table/close-cargoplace/draft',
      { cargoplace_id: '01929c5e-cp-1' }
    )
  })
})

describe('submitCloseCargoplace', () => {
  it('POSTs the operation id and snake_case assertions, maps the close result', async () => {
    apiMocks.apiClient.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          cargoplace_id: '01929c5e-cp-1',
          status: 'TABLE_CLOSED',
          summary: {
            products_received: 2,
            products_expected: 2,
            shortage: 0,
            shortage_by_sku: null,
          },
          outbox_events_created: 3,
          operation_id: '01929c5e-op-1',
        },
        error: null,
      },
    })

    expect(
      await submitCloseCargoplace('01929c5e-op-1', [
        {
          actionId: '01929c5e-a-1',
          credentialId: 'AQ',
          authenticatorData: 'Ag',
          clientDataJson: 'Aw',
          signature: 'BA',
        },
      ])
    ).toEqual({
      cargoplaceId: '01929c5e-cp-1',
      status: 'TABLE_CLOSED',
      summary: {
        productsReceived: 2,
        productsExpected: 2,
        shortage: 0,
        shortageBySku: [],
      },
      outboxEventsCreated: 3,
      operationId: '01929c5e-op-1',
    })
    expect(apiMocks.apiClient.post).toHaveBeenCalledWith(
      '/receiving/table/close-cargoplace/submit',
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
