import { isAxiosError } from 'axios'

import { webauthnErrorText } from './webauthn'

const FALLBACK = 'Не удалось выполнить операцию'

const CODE_TEXTS: Record<string, string> = {
  SIGNER_KEY_NOT_ACTIVE: 'Ваш ключ ещё не активен в цепи',
  TARGET_NOT_PENDING: 'Ключ уже подтверждён, отклонён или отозван',
  ACTION_IN_PROGRESS: 'Подтверждение уже отправлено',
  ACTION_ALREADY_SUBMITTED: 'Подтверждение уже отправлено',
  ACTION_NOT_FOUND: 'Черновик подтверждения не найден, попробуйте ещё раз',
  INVALID_ASSERTION: 'Подпись не прошла проверку',
}

// Axios rejects non-2xx before unwrap runs, so the backend envelope message has
// to be read from the response body; plain Errors (from unwrap) carry it already.
export function passkeyErrorText(error: unknown): string {
  const webauthn = webauthnErrorText(error)
  if (webauthn) return webauthn
  if (isAxiosError(error)) {
    const body = (
      error.response?.data as { error?: { code?: string; message?: string } } | undefined
    )?.error
    const codeText = body?.code ? CODE_TEXTS[body.code] : undefined
    return codeText ?? body?.message ?? FALLBACK
  }
  if (error instanceof Error) return error.message
  return FALLBACK
}
