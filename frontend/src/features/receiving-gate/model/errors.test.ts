import { describe, expect, it } from 'vitest'

import {
  getReceivingErrorCode,
  getReceivingErrorMessage,
  isTerminalShipmentError,
} from './errors'

// Minimal AxiosError-shaped fixture; axios.isAxiosError only checks the flag.
function axiosError(status: number | undefined, data: unknown, code?: string) {
  return { isAxiosError: true, code, response: status ? { status, data } : undefined }
}

function envelope(code: string, message: string) {
  return { success: false, data: null, error: { code, message } }
}

describe('getReceivingErrorMessage', () => {
  it('prefers the server-provided envelope message', () => {
    const error = axiosError(404, envelope('TTN_NOT_FOUND', 'Поставка не найдена'))
    expect(getReceivingErrorMessage(error)).toBe('Поставка не найдена')
  })

  it('handles a 401 plain-text body without crashing on data.error', () => {
    const error = axiosError(401, 'Unauthorized')
    expect(getReceivingErrorMessage(error)).toContain('Сессия истекла')
  })

  it('maps 403 / 5xx / network errors to friendly fallbacks', () => {
    expect(getReceivingErrorMessage(axiosError(403, ''))).toContain('Недостаточно прав')
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
      getReceivingErrorCode(axiosError(409, envelope('SHIPMENT_NOT_IN_PROGRESS', 'x'))),
    ).toBe('SHIPMENT_NOT_IN_PROGRESS')
  })

  it('returns null for plain-text and non-axios errors', () => {
    expect(getReceivingErrorCode(axiosError(401, 'Unauthorized'))).toBeNull()
    expect(getReceivingErrorCode(new Error('boom'))).toBeNull()
  })
})

describe('isTerminalShipmentError', () => {
  it('is true only for shipment-level terminal codes', () => {
    for (const code of [
      'SHIPMENT_NOT_IN_PROGRESS',
      'SHIPMENT_NOT_FOUND',
      'SHIPMENT_ALREADY_CLOSED',
    ]) {
      expect(isTerminalShipmentError(axiosError(409, envelope(code, 'x')))).toBe(true)
    }
  })

  it('is false for cargoplace-level errors and uncoded failures', () => {
    expect(
      isTerminalShipmentError(axiosError(409, envelope('CARGOPLACE_ALREADY_RECEIVED', 'x'))),
    ).toBe(false)
    expect(
      isTerminalShipmentError(axiosError(400, envelope('CARGOPLACE_NOT_IN_SHIPMENT', 'x'))),
    ).toBe(false)
    expect(isTerminalShipmentError(axiosError(500, ''))).toBe(false)
    expect(isTerminalShipmentError(new Error('boom'))).toBe(false)
  })
})
