import { describe, expect, it } from 'vitest'

import { mapOrgKeys, mapPasskey } from './passkeyApi'

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

describe('mapOrgKeys', () => {
  it('maps organization and members, keeping passkey null for members without a key', () => {
    const keys = mapOrgKeys({
      organization: { org_id: 'org-1', name: 'Acme' },
      members: [
        { user_id: 'u1', username: 'boss', role: 'ADMIN', passkey: null },
      ],
    })
    expect(keys).toEqual({
      organization: { orgId: 'org-1', name: 'Acme' },
      members: [{ userId: 'u1', username: 'boss', role: 'ADMIN', passkey: null }],
    })
  })
})
