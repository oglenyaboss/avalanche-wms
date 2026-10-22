import { apiClient } from '@/shared/api'

import type {
  AdminKeyExport,
  AssertionJSON,
  ConfirmationState,
  KeyActionDraft,
  KeyConfirmation,
  OrgKeys,
  Passkey,
  PasskeyRole,
  PasskeyStatus,
} from '../model/types'
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
    confirmation: KeyConfirmationResponse | null
  }[]
}

interface KeyConfirmationResponse {
  action_id: string
  action_type: number
  state: ConfirmationState
  reject_reason: number | null
}

interface KeyActionDraftResponse {
  action_id: string
  action_type: number
  challenge: string
  rp_id: string
  allow_credentials: { type: 'public-key'; id: string }[]
  user_verification: 'required'
  timeout: number
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

export function mapKeyConfirmation(c: KeyConfirmationResponse): KeyConfirmation {
  return {
    actionId: c.action_id,
    actionType: c.action_type,
    state: c.state,
    rejectReason: c.reject_reason,
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
      confirmation: m.confirmation ? mapKeyConfirmation(m.confirmation) : null,
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

export function mapKeyActionDraft(d: KeyActionDraftResponse): KeyActionDraft {
  return {
    actionId: d.action_id,
    actionType: d.action_type,
    challenge: d.challenge,
    rpId: d.rp_id,
    allowCredentials: d.allow_credentials,
    userVerification: d.user_verification,
    timeout: d.timeout,
  }
}

export async function getOrganizationKeys(): Promise<OrgKeys> {
  const { data } = await apiClient.get<ApiEnvelope<OrgKeysResponse>>('/passkeys/organization')
  return mapOrgKeys(unwrap(data))
}

export async function draftKeyConfirmation(targetKeyId: string): Promise<KeyActionDraft> {
  const { data } = await apiClient.post<ApiEnvelope<KeyActionDraftResponse>>(
    '/passkeys/actions/key-register/draft',
    { target_key_id: targetKeyId }
  )
  return mapKeyActionDraft(unwrap(data))
}

export async function draftKeyRevoke(targetKeyId: string): Promise<KeyActionDraft> {
  const { data } = await apiClient.post<ApiEnvelope<KeyActionDraftResponse>>(
    '/passkeys/actions/key-revoke/draft',
    { target_key_id: targetKeyId }
  )
  return mapKeyActionDraft(unwrap(data))
}

export async function rejectKey(targetKeyId: string): Promise<void> {
  await apiClient.post('/passkeys/organization/keys/reject', { target_key_id: targetKeyId })
}

export async function submitKeyConfirmation(
  actionId: string,
  assertion: AssertionJSON
): Promise<KeyConfirmation> {
  const { data } = await apiClient.post<ApiEnvelope<KeyConfirmationResponse>>(
    `/passkeys/actions/${actionId}/submit`,
    {
      credential_id: assertion.credentialId,
      authenticator_data: assertion.authenticatorData,
      client_data_json: assertion.clientDataJson,
      signature: assertion.signature,
    }
  )
  return mapKeyConfirmation(unwrap(data))
}

export async function getAdminKeyExport(): Promise<AdminKeyExport> {
  const { data } = await apiClient.get<ApiEnvelope<AdminKeyExport>>(
    '/passkeys/organization/admin-key'
  )
  return unwrap(data)
}
