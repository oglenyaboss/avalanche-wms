import type { PasskeyRole, PasskeyStatus } from './types'

const STATUS_LABELS: Record<PasskeyStatus, string> = {
  PENDING: 'Ожидает подтверждения',
  ACTIVE: 'Активен в цепи',
  REJECTED: 'Отклонён',
  REVOKED: 'Отозван',
}

export function passkeyStatusLabel(status: PasskeyStatus): string {
  return STATUS_LABELS[status]
}

const PENDING_HINTS: Record<PasskeyRole, string> = {
  ADMIN: 'Ключ станет активным, когда владелец консорциума зарегистрирует его в реестре.',
  OPERATOR: 'Ключ станет активным, когда его подтвердит админ организации.',
}

export function pendingKeyHint(role: PasskeyRole): string {
  return PENDING_HINTS[role]
}

export function shortKeyId(keyId: string): string {
  return `${keyId.slice(0, 6)}…${keyId.slice(-4)}`
}
