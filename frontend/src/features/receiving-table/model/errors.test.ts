import { describe, expect, it } from 'vitest'

import {
  getReceivingErrorCode,
  getReceivingErrorMessage,
  isBoxTerminalError,
  isCargoplaceTerminalError,
} from './errors'

// Minimal AxiosError-shaped fixture; axios.isAxiosError only checks the flag.
function axiosError(status: number | undefined, data: unknown, code?: string) {
  return {
    isAxiosError: true,
    code,
    response: status ? { status, data } : undefined,
  }
}

function envelope(code: string, message: string) {
  return { success: false, data: null, error: { code, message } }
}

describe('getReceivingErrorMessage', () => {
  it('prefers the server-provided envelope message', () => {
    const error = axiosError(
      404,
      envelope('CARGOPLACE_NOT_FOUND', 'Грузоместо не найдено'),
    )
    expect(getReceivingErrorMessage(error)).toBe('Грузоместо не найдено')
  })

  it('handles a 401 plain-text body without crashing on data.error', () => {
    const error = axiosError(401, 'Unauthorized')
    expect(getReceivingErrorMessage(error)).toContain('Сессия истекла')
  })

  it('maps 403 / 5xx / network errors to friendly fallbacks', () => {
    expect(getReceivingErrorMessage(axiosError(403, ''))).toContain(
      'Недостаточно прав',
    )
    expect(getReceivingErrorMessage(axiosError(500, ''))).toContain('сервера')
    expect(
      getReceivingErrorMessage(axiosError(undefined, undefined, 'ERR_NETWORK')),
    ).toContain('соединения')
  })

  it('falls back to a generic message for non-axios errors', () => {
    expect(getReceivingErrorMessage(new Error('boom'))).toBe(
      'Не удалось выполнить операцию. Повторите попытку.',
    )
  })
})

describe('getReceivingErrorCode', () => {
  it('extracts the envelope error code', () => {
    expect(
      getReceivingErrorCode(
        axiosError(409, envelope('CARGOPLACE_NOT_IN_PROGRESS', 'x')),
      ),
    ).toBe('CARGOPLACE_NOT_IN_PROGRESS')
  })

  it('returns null for plain-text and non-axios errors', () => {
    expect(getReceivingErrorCode(axiosError(401, 'Unauthorized'))).toBeNull()
    expect(getReceivingErrorCode(new Error('boom'))).toBeNull()
  })
})

describe('isCargoplaceTerminalError', () => {
  it('is true only for cargoplace-level terminal codes', () => {
    for (const code of [
      'CARGOPLACE_NOT_FOUND',
      'CARGOPLACE_NOT_RECEIVED_AT_GATE',
      'CARGOPLACE_NOT_IN_PROGRESS',
    ]) {
      expect(
        isCargoplaceTerminalError(axiosError(409, envelope(code, 'x'))),
      ).toBe(true)
    }
  })

  it('is false for box-level errors and uncoded failures', () => {
    expect(
      isCargoplaceTerminalError(axiosError(409, envelope('BOX_NOT_OPEN', 'x'))),
    ).toBe(false)
    expect(
      isCargoplaceTerminalError(
        axiosError(409, envelope('QR_ALREADY_EXISTS', 'x')),
      ),
    ).toBe(false)
    expect(isCargoplaceTerminalError(axiosError(500, ''))).toBe(false)
    expect(isCargoplaceTerminalError(new Error('boom'))).toBe(false)
  })
})

describe('isBoxTerminalError', () => {
  it('is true only for box-level codes', () => {
    for (const code of [
      'BOX_NOT_FOUND',
      'BOX_NOT_OPEN',
      'BOX_NOT_IN_CARGOPLACE',
    ]) {
      expect(isBoxTerminalError(axiosError(409, envelope(code, 'x')))).toBe(true)
    }
  })

  it('is false for cargoplace-level and uncoded failures', () => {
    expect(
      isBoxTerminalError(axiosError(409, envelope('CARGOPLACE_NOT_FOUND', 'x'))),
    ).toBe(false)
    expect(
      isBoxTerminalError(axiosError(409, envelope('QR_ALREADY_EXISTS', 'x'))),
    ).toBe(false)
    expect(isBoxTerminalError(new Error('boom'))).toBe(false)
  })
})
