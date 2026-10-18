import { describe, expect, it } from 'vitest'

import { mapKeyActionDraft, mapKeyConfirmation, mapOrgKeys, mapPasskey } from './passkeyApi'

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
    expect(mapKeyConfirmation({ action_id: 'a-1', state: 'SENT', reject_reason: null })).toEqual({
      actionId: 'a-1',
      state: 'SENT',
      rejectReason: null,
    })
    expect(mapKeyConfirmation({ action_id: 'a-2', state: 'REJECTED', reject_reason: 5 })).toEqual({
      actionId: 'a-2',
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
          confirmation: { action_id: 'a-3', state: 'SENT', reject_reason: null },
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
          confirmation: { actionId: 'a-3', state: 'SENT', rejectReason: null },
        },
      ],
    })
  })
})
