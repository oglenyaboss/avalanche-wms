import { describe, expect, it } from 'vitest'

import { getAssemblyErrorMessage } from './errors'

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

describe('getAssemblyErrorMessage', () => {
  it('prefers the server-provided envelope message', () => {
    const error = axiosError(
      409,
      envelope('PRODUCT_NOT_ALLOCATED', 'Товар не аллоцирован под заказ'),
    )
    expect(getAssemblyErrorMessage(error)).toBe('Товар не аллоцирован под заказ')
  })

  it('handles a 401 plain-text body without crashing on data.error', () => {
    const error = axiosError(401, 'Unauthorized')
    expect(getAssemblyErrorMessage(error)).toContain('Сессия истекла')
  })

  it('maps 403 / 5xx / network errors to friendly fallbacks', () => {
    expect(getAssemblyErrorMessage(axiosError(403, ''))).toContain(
      'только оператору',
    )
    expect(getAssemblyErrorMessage(axiosError(500, ''))).toContain('сервера')
    expect(
      getAssemblyErrorMessage(axiosError(undefined, undefined, 'ERR_NETWORK')),
    ).toContain('соединения')
  })

  it('falls back to a generic message for non-axios errors', () => {
    expect(getAssemblyErrorMessage(new Error('boom'))).toBe(
      'Не удалось выполнить операцию. Повторите попытку.',
    )
  })
})
