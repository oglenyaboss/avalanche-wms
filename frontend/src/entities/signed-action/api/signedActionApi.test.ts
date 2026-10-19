import { describe, expect, it, vi } from 'vitest'

import type { PartAssertion } from '../model/types'

const apiMocks = vi.hoisted(() => ({
  apiClient: { get: vi.fn(), post: vi.fn() },
}))

vi.mock('@/shared/api', () => apiMocks)

import { getOperationStatus, mapSignedOperationDraft, toAssertionsBody } from './signedActionApi'

describe('mapSignedOperationDraft', () => {
  it('maps the draft fields needed for signing, one entry per part', () => {
    expect(
      mapSignedOperationDraft({
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
            item_ids: ['01929c5e-i-1', '01929c5e-i-2'],
            details_hash: '0xdeadbeef',
            challenge: 'AQID',
          },
          {
            action_id: '01929c5e-a-2',
            item_ids: [],
            details_hash: '0xfeedface',
            challenge: 'BAUG',
          },
        ],
      })
    ).toEqual({
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
          itemIds: ['01929c5e-i-1', '01929c5e-i-2'],
          detailsHash: '0xdeadbeef',
          challenge: 'AQID',
        },
        {
          actionId: '01929c5e-a-2',
          itemIds: [],
          detailsHash: '0xfeedface',
          challenge: 'BAUG',
        },
      ],
    })
  })
})

describe('toAssertionsBody', () => {
  it('maps one assertion per part to the snake_case submit body entries', () => {
    const assertions: PartAssertion[] = [
      {
        actionId: '01929c5e-a-1',
        credentialId: 'AQ',
        authenticatorData: 'Ag',
        clientDataJson: 'Aw',
        signature: 'BA',
      },
    ]

    expect(toAssertionsBody(assertions)).toEqual([
      {
        action_id: '01929c5e-a-1',
        credential_id: 'AQ',
        authenticator_data: 'Ag',
        client_data_json: 'Aw',
        signature: 'BA',
      },
    ])
  })
})

describe('getOperationStatus', () => {
  it('GETs the operation endpoint and maps the per-part chain states', async () => {
    apiMocks.apiClient.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          operation_id: '01929c5e-op-1',
          parts: [
            { action_id: '01929c5e-a-1', state: 'APPLIED', reject_reason: null },
            { action_id: '01929c5e-a-2', state: 'REJECTED', reject_reason: 12 },
          ],
        },
        error: null,
      },
    })

    expect(await getOperationStatus('01929c5e-op-1')).toEqual({
      operationId: '01929c5e-op-1',
      parts: [
        { actionId: '01929c5e-a-1', state: 'APPLIED', rejectReason: null },
        { actionId: '01929c5e-a-2', state: 'REJECTED', rejectReason: 12 },
      ],
    })
    expect(apiMocks.apiClient.get).toHaveBeenCalledWith(
      '/signed-actions/operations/01929c5e-op-1'
    )
  })
})
