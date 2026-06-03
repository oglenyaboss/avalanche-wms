import { describe, expect, it } from 'vitest'

import { getPutawayErrorCode, getPutawayErrorMessage } from './errors'

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

describe('getPutawayErrorMessage', () => {
  it('prefers the server-provided envelope message', () => {
    const error = axiosError(
      409,
      envelope('PRODUCT_NOT_IN_BUFFER', 'Товар не в буферной ячейке'),
    )
    expect(getPutawayErrorMessage(error)).toBe('Товар не в буферной ячейке')
  })

  it('handles a 401 plain-text body without crashing on data.error', () => {
    const error = axiosError(401, 'Unauthorized')
    expect(getPutawayErrorMessage(error)).toContain('Сессия истекла')
  })

  it('maps 403 / 5xx / network errors to friendly fallbacks', () => {
    expect(getPutawayErrorMessage(axiosError(403, ''))).toContain(
      'только оператору',
    )
    expect(getPutawayErrorMessage(axiosError(500, ''))).toContain('сервера')
    expect(
      getPutawayErrorMessage(axiosError(undefined, undefined, 'ERR_NETWORK')),
    ).toContain('соединения')
  })

  it('falls back to a generic message for non-axios errors', () => {
    expect(getPutawayErrorMessage(new Error('boom'))).toBe(
      'Не удалось выполнить операцию. Повторите попытку.',
    )
  })
})

describe('getPutawayErrorCode', () => {
  it('extracts the envelope error code', () => {
    expect(
      getPutawayErrorCode(
        axiosError(409, envelope('CHAIN_EVENT_REJECTED', 'x')),
      ),
    ).toBe('CHAIN_EVENT_REJECTED')
  })

  it('returns null for plain-text and non-axios errors', () => {
    expect(getPutawayErrorCode(axiosError(401, 'Unauthorized'))).toBeNull()
    expect(getPutawayErrorCode(new Error('boom'))).toBeNull()
  })
})
