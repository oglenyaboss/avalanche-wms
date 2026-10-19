import { describe, expect, it } from 'vitest'

import {
  confirmationText,
  passkeyStatusLabel,
  pendingKeyHint,
  rejectReasonText,
  shortKeyId,
} from './status'

describe('passkeyStatusLabel', () => {
  it('returns Russian labels for every status', () => {
    expect(passkeyStatusLabel('PENDING')).toBe('Ожидает подтверждения')
    expect(passkeyStatusLabel('ACTIVE')).toBe('Активен в цепи')
    expect(passkeyStatusLabel('REJECTED')).toBe('Отклонён')
    expect(passkeyStatusLabel('REVOKED')).toBe('Отозван')
  })
})

describe('pendingKeyHint', () => {
  it('names who activates the key for each role', () => {
    expect(pendingKeyHint('ADMIN')).toBe('Ключ станет активным, когда владелец консорциума зарегистрирует его в реестре.')
    expect(pendingKeyHint('OPERATOR')).toBe('Ключ станет активным, когда его подтвердит админ организации.')
  })
})

describe('rejectReasonText', () => {
  it('labels known reject reasons in Russian', () => {
    expect(rejectReasonText(1)).toBe('Ключ подписанта неизвестен реестру')
    expect(rejectReasonText(3)).toBe('Подпись не прошла проверку в цепи')
    expect(rejectReasonText(9)).toBe('Ключ не принадлежит оператору')
  })

  it('falls back to the raw code for unknown reasons and to a neutral text for null', () => {
    expect(rejectReasonText(12)).toBe('Код 12')
    expect(rejectReasonText(null)).toBe('Причина неизвестна')
  })
})

describe('confirmationText', () => {
  it('labels each confirmation state, with the reason for rejections', () => {
    expect(confirmationText({ actionId: 'a', state: 'SENT', rejectReason: null })).toBe(
      'Отправлено в цепь'
    )
    expect(confirmationText({ actionId: 'a', state: 'APPLIED', rejectReason: null })).toBe(
      'Подтверждено в цепи'
    )
    expect(confirmationText({ actionId: 'a', state: 'REJECTED', rejectReason: 5 })).toBe(
      'Отклонено: Подписант не админ организации'
    )
    expect(confirmationText({ actionId: 'a', state: 'FAILED', rejectReason: null })).toBe(
      'Не удалось записать в цепь'
    )
  })
})

describe('shortKeyId', () => {
  it('keeps the first 6 and last 4 characters', () => {
    expect(shortKeyId('0x1234567890abcdef')).toBe('0x1234…cdef')
  })
})
