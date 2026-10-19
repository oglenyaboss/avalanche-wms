import { rejectReasonText } from '@/shared/lib'

import type { KeyConfirmation, PasskeyRole, PasskeyStatus } from './types'

// Reject-reason labels live in @/shared/lib (shared with the signed-action
// close flow); re-exported so the passkey public API stays stable.
export { rejectReasonText }

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

export function confirmationText(c: KeyConfirmation): string {
  switch (c.state) {
    case 'SENT':
      return 'Отправлено в цепь'
    case 'APPLIED':
      return 'Подтверждено в цепи'
    case 'REJECTED':
      return `Отклонено: ${rejectReasonText(c.rejectReason)}`
    case 'FAILED':
      return 'Не удалось записать в цепь'
  }
}

export function shortKeyId(keyId: string): string {
  return `${keyId.slice(0, 6)}…${keyId.slice(-4)}`
}
