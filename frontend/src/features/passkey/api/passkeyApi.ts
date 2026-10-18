import { apiClient } from '@/shared/api'

import type { AdminKeyExport, OrgKeys, Passkey, PasskeyRole, PasskeyStatus } from '../model/types'
import type { CreationOptionsJSON, RegistrationResponseJSON } from '../model/webauthn'

interface ApiEnvelope<T> {
  success: boolean
  data: T | null
  error: { code: string; message: string } | null
}

interface PasskeyResponse {
  credential_id: string
  key_id: string
  qx: string
  qy: string
  role: PasskeyRole
  status: PasskeyStatus
  created_at: string
}

interface OrgKeysResponse {
  organization: { org_id: string; name: string }
  members: {
    user_id: string
    username: string
    role: PasskeyRole
    passkey: PasskeyResponse | null
  }[]
}

function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }
  return envelope.data
}

export function mapPasskey(p: PasskeyResponse): Passkey {
  return {
    credentialId: p.credential_id,
    keyId: p.key_id,
    qx: p.qx,
    qy: p.qy,
    role: p.role,
    status: p.status,
    createdAt: p.created_at,
  }
}

export function mapOrgKeys(r: OrgKeysResponse): OrgKeys {
  return {
    organization: { orgId: r.organization.org_id, name: r.organization.name },
    members: r.members.map((m) => ({
      userId: m.user_id,
      username: m.username,
      role: m.role,
      passkey: m.passkey ? mapPasskey(m.passkey) : null,
    })),
  }
}

export async function beginRegistration(): Promise<CreationOptionsJSON> {
  const { data } = await apiClient.post<ApiEnvelope<{ publicKey: CreationOptionsJSON }>>(
    '/passkeys/registration/begin'
  )
  return unwrap(data).publicKey
}

export async function finishRegistration(body: RegistrationResponseJSON): Promise<Passkey> {
  const { data } = await apiClient.post<ApiEnvelope<PasskeyResponse>>(
    '/passkeys/registration/finish',
    body
  )
  return mapPasskey(unwrap(data))
}

// No key is a valid answer here (200 with data null), not an error.
export async function getMyPasskey(): Promise<Passkey | null> {
  const { data } = await apiClient.get<ApiEnvelope<PasskeyResponse>>('/passkeys/me')
  if (!data.success) {
    throw new Error(data.error?.message ?? 'Некорректный ответ сервера')
  }
  return data.data ? mapPasskey(data.data) : null
}

export async function getOrganizationKeys(): Promise<OrgKeys> {
  const { data } = await apiClient.get<ApiEnvelope<OrgKeysResponse>>('/passkeys/organization')
  return mapOrgKeys(unwrap(data))
}

export async function getAdminKeyExport(): Promise<AdminKeyExport> {
  const { data } = await apiClient.get<ApiEnvelope<AdminKeyExport>>(
    '/passkeys/organization/admin-key'
  )
  return unwrap(data)
}
