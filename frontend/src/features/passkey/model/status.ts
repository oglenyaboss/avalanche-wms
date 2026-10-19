import { rejectReasonText } from '@/shared/lib'

import type { KeyConfirmation, PasskeyRole, PasskeyStatus } from './types'

// Reject-reason labels live in @/shared/lib (shared with the signed-action
// close flow); re-exported so the passkey public API stays stable.
export { rejectReasonText }

const STATUS_LABELS: Record<PasskeyStatus, string> = {
  PENDING: 'Ожидает подтверждения',
  ACTIVE: 'Активен в цепи',
  REJECTED: 'Отклонён',
  REVOKED: 'Отозван в цепи',
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

// Action-type-specific confirmation texts: 5 = KeyRegister, 6 = KeyRevoke.
interface ConfirmationLabels {
  SENT: string
  APPLIED: string
  rejectedPrefix: string
  FAILED: string
}

const CONFIRMATION_LABELS: Record<number, ConfirmationLabels> = {
  5: {
    SENT: 'Отправлено в цепь',
    APPLIED: 'Подтверждено в цепи',
    rejectedPrefix: 'Отклонено',
    FAILED: 'Не удалось записать в цепь',
  },
  6: {
    SENT: 'Отзыв отправлен в цепь',
    APPLIED: 'Отозван в цепи',
    rejectedPrefix: 'Отзыв отклонён',
    FAILED: 'Не удалось записать отзыв в цепь',
  },
}

export function confirmationText(c: KeyConfirmation): string {
  const labels = CONFIRMATION_LABELS[c.actionType] ?? CONFIRMATION_LABELS[5]
  switch (c.state) {
    case 'SENT':
      return labels.SENT
    case 'APPLIED':
      return labels.APPLIED
    case 'REJECTED':
      return `${labels.rejectedPrefix}: ${rejectReasonText(c.rejectReason)}`
    case 'FAILED':
      return labels.FAILED
  }
}

export function shortKeyId(keyId: string): string {
  return `${keyId.slice(0, 6)}…${keyId.slice(-4)}`
}
