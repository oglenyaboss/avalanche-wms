import { describe, expect, it } from 'vitest'

import { passkeyStatusLabel, shortKeyId } from './status'

describe('passkeyStatusLabel', () => {
  it('returns Russian labels for every status', () => {
    expect(passkeyStatusLabel('PENDING')).toBe('Ожидает подтверждения')
    expect(passkeyStatusLabel('ACTIVE')).toBe('Активен в цепи')
    expect(passkeyStatusLabel('REJECTED')).toBe('Отклонён')
    expect(passkeyStatusLabel('REVOKED')).toBe('Отозван')
  })
})

describe('shortKeyId', () => {
  it('keeps the first 6 and last 4 characters', () => {
    expect(shortKeyId('0x1234567890abcdef')).toBe('0x1234…cdef')
  })
})
