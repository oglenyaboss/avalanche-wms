import { describe, expect, it, vi } from 'vitest'

const apiMocks = vi.hoisted(() => ({
  apiClient: { post: vi.fn() },
}))

vi.mock('@/shared/api', () => apiMocks)

import { draftKeyRevoke, mapKeyActionDraft, mapKeyConfirmation, mapOrgKeys, mapPasskey } from './passkeyApi'

describe('mapPasskey', () => {
  it('maps snake_case to camelCase', () => {
    expect(
      mapPasskey({
        credential_id: 'cred',
        key_id: '0xabc',
        qx: '0x1',
        qy: '0x2',
        role: 'ADMIN',
        status: 'PENDING',
        created_at: '2026-10-01T10:00:00Z',
      })
    ).toEqual({
      credentialId: 'cred',
      keyId: '0xabc',
      qx: '0x1',
      qy: '0x2',
      role: 'ADMIN',
      status: 'PENDING',
      createdAt: '2026-10-01T10:00:00Z',
    })
  })
})

describe('mapKeyConfirmation', () => {
  it('maps snake_case to camelCase, keeping null reject reason', () => {
    expect(
      mapKeyConfirmation({ action_id: 'a-1', action_type: 5, state: 'SENT', reject_reason: null })
    ).toEqual({
      actionId: 'a-1',
      actionType: 5,
      state: 'SENT',
      rejectReason: null,
    })
    expect(
      mapKeyConfirmation({ action_id: 'a-2', action_type: 6, state: 'REJECTED', reject_reason: 5 })
    ).toEqual({
      actionId: 'a-2',
      actionType: 6,
      state: 'REJECTED',
      rejectReason: 5,
    })
  })
})

describe('mapKeyActionDraft', () => {
  it('maps the draft fields needed for an assertion', () => {
    expect(
      mapKeyActionDraft({
        action_id: '01929c5e-6f00-7000-8000-000000001001',
        action_type: 5,
        challenge: '-_8',
        rp_id: 'localhost',
        allow_credentials: [{ type: 'public-key', id: 'AQID' }],
        user_verification: 'required',
        timeout: 60000,
      })
    ).toEqual({
      actionId: '01929c5e-6f00-7000-8000-000000001001',
      actionType: 5,
      challenge: '-_8',
      rpId: 'localhost',
      allowCredentials: [{ type: 'public-key', id: 'AQID' }],
      userVerification: 'required',
      timeout: 60000,
    })
  })
})

describe('draftKeyRevoke', () => {
  it('posts the target key id to the revoke draft endpoint and maps the draft', async () => {
    apiMocks.apiClient.post.mockResolvedValue({
      data: {
        success: true,
        error: null,
        data: {
          action_id: '01929c5e-6f00-7000-8000-000000002001',
          action_type: 6,
          challenge: 'AQID',
          rp_id: 'localhost',
          allow_credentials: [{ type: 'public-key', id: 'AQID' }],
          user_verification: 'required',
          timeout: 60000,
        },
      },
    })

    await expect(draftKeyRevoke('0xfeed000000000000beef')).resolves.toEqual({
      actionId: '01929c5e-6f00-7000-8000-000000002001',
      actionType: 6,
      challenge: 'AQID',
      rpId: 'localhost',
      allowCredentials: [{ type: 'public-key', id: 'AQID' }],
      userVerification: 'required',
      timeout: 60000,
    })
    expect(apiMocks.apiClient.post).toHaveBeenCalledWith('/passkeys/actions/key-revoke/draft', {
      target_key_id: '0xfeed000000000000beef',
    })
  })
})

describe('mapOrgKeys', () => {
  it('maps organization, members and confirmations, keeping nulls for members without a key', () => {
    const keys = mapOrgKeys({
      organization: { org_id: 'org-1', name: 'Acme' },
      members: [
        { user_id: 'u1', username: 'boss', role: 'ADMIN', passkey: null, confirmation: null },
        {
          user_id: 'u2',
          username: 'op',
          role: 'OPERATOR',
          passkey: {
            credential_id: 'cred-2',
            key_id: '0xdef',
            qx: '0x3',
            qy: '0x4',
            role: 'OPERATOR',
            status: 'PENDING',
            created_at: '2026-10-02T10:00:00Z',
          },
          confirmation: { action_id: 'a-3', action_type: 5, state: 'SENT', reject_reason: null },
        },
      ],
    })
    expect(keys).toEqual({
      organization: { orgId: 'org-1', name: 'Acme' },
      members: [
        { userId: 'u1', username: 'boss', role: 'ADMIN', passkey: null, confirmation: null },
        {
          userId: 'u2',
          username: 'op',
          role: 'OPERATOR',
          passkey: {
            credentialId: 'cred-2',
            keyId: '0xdef',
            qx: '0x3',
            qy: '0x4',
            role: 'OPERATOR',
            status: 'PENDING',
            createdAt: '2026-10-02T10:00:00Z',
          },
          confirmation: { actionId: 'a-3', actionType: 5, state: 'SENT', rejectReason: null },
        },
      ],
    })
  })
})
