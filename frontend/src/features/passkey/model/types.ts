export type PasskeyRole = 'ADMIN' | 'OPERATOR'

export type PasskeyStatus = 'PENDING' | 'ACTIVE' | 'REJECTED' | 'REVOKED'

export interface Passkey {
  credentialId: string
  keyId: string
  qx: string
  qy: string
  role: PasskeyRole
  status: PasskeyStatus
  createdAt: string
}

export type ConfirmationState = 'SENT' | 'APPLIED' | 'REJECTED' | 'FAILED'

export interface KeyConfirmation {
  actionId: string
  state: ConfirmationState
  rejectReason: number | null
}

export interface OrgMember {
  userId: string
  username: string
  role: PasskeyRole
  passkey: Passkey | null
  confirmation: KeyConfirmation | null
}

export interface OrgKeys {
  organization: { orgId: string; name: string }
  members: OrgMember[]
}

export interface AdminKeyExport {
  orgId: string
  name: string
  qx: string
  qy: string
}

export interface KeyActionDraft {
  actionId: string
  actionType: number
  challenge: string
  rpId: string
  allowCredentials: { type: 'public-key'; id: string }[]
  userVerification: 'required'
  timeout: number
}

// The assertion wire shape is shared with the signed-action flow; the
// canonical definition lives in @/shared/lib.
export type { AssertionJSON } from '@/shared/lib'
