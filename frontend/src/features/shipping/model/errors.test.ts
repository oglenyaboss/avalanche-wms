import { describe, expect, it } from 'vitest'

import { getShippingErrorMessage } from './errors'

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

describe('getShippingErrorMessage', () => {
  it('prefers the server-provided envelope message', () => {
    const error = axiosError(
      409,
      envelope('DESTINATION_MISMATCH', 'Рейс назначен на другую зону'),
    )
    expect(getShippingErrorMessage(error)).toBe('Рейс назначен на другую зону')
  })

  it('handles a 401 plain-text body without crashing on data.error', () => {
    const error = axiosError(401, 'unauthorized')
    expect(getShippingErrorMessage(error)).toContain('Сессия истекла')
  })

  it('maps 403 / 5xx / network errors to friendly fallbacks', () => {
    expect(getShippingErrorMessage(axiosError(403, ''))).toContain(
      'только оператору',
    )
    expect(getShippingErrorMessage(axiosError(500, ''))).toContain('сервера')
    expect(
      getShippingErrorMessage(axiosError(undefined, undefined, 'ERR_NETWORK')),
    ).toContain('соединения')
  })

  it('falls back to a generic message for an unknown error', () => {
    expect(getShippingErrorMessage(new Error('nope'))).toContain(
      'Не удалось выполнить операцию',
    )
  })
})
