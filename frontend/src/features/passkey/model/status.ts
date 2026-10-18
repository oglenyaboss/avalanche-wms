import type { PasskeyStatus } from './types'

const STATUS_LABELS: Record<PasskeyStatus, string> = {
  PENDING: 'Ожидает подтверждения',
  ACTIVE: 'Активен в цепи',
  REJECTED: 'Отклонён',
  REVOKED: 'Отозван',
}

export function passkeyStatusLabel(status: PasskeyStatus): string {
  return STATUS_LABELS[status]
}

export function shortKeyId(keyId: string): string {
  return `${keyId.slice(0, 6)}…${keyId.slice(-4)}`
}
