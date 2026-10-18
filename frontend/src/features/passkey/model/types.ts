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

export interface OrgMember {
  userId: string
  username: string
  role: PasskeyRole
  passkey: Passkey | null
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
