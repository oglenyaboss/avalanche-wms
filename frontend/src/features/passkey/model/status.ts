import type { KeyConfirmation, PasskeyRole, PasskeyStatus } from './types'

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

const REJECT_REASON_LABELS: Record<number, string> = {
  1: 'Ключ подписанта неизвестен реестру',
  2: 'Ключ подписанта отозван',
  3: 'Подпись не прошла проверку в цепи',
  4: 'Неверный тип действия',
  5: 'Подписант не админ организации',
  6: 'Ключ уже зарегистрирован в реестре',
  7: 'Некорректный публичный ключ',
  8: 'Ключ из другой организации',
  9: 'Ключ не принадлежит оператору',
}

export function rejectReasonText(code: number | null): string {
  if (code === null) return 'Причина неизвестна'
  return REJECT_REASON_LABELS[code] ?? `Код ${code}`
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
