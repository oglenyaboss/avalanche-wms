import { isAxiosError } from 'axios'

import { webauthnErrorText } from './webauthn'

const FALLBACK = 'Не удалось выполнить операцию'

// Axios rejects non-2xx before unwrap runs, so the backend envelope message has
// to be read from the response body; plain Errors (from unwrap) carry it already.
export function passkeyErrorText(error: unknown): string {
  const webauthn = webauthnErrorText(error)
  if (webauthn) return webauthn
  if (isAxiosError(error)) {
    const msg = (error.response?.data as { error?: { message?: string } } | undefined)?.error
      ?.message
    return msg ?? FALLBACK
  }
  if (error instanceof Error) return error.message
  return FALLBACK
}
