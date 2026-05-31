import { isAxiosError } from 'axios'

// Codes that mean the cargoplace can no longer be processed at the table. When
// the API returns one of these, the operator must restart from the cargoplace
// scan (CARGOPLACE_NOT_IN_PROGRESS can surface mid-flow if the cargoplace was
// closed elsewhere, or on a duplicate close).
const CARGOPLACE_TERMINAL_CODES = [
  'CARGOPLACE_NOT_FOUND',
  'CARGOPLACE_NOT_RECEIVED_AT_GATE',
  'CARGOPLACE_NOT_IN_PROGRESS',
]

// Codes that mean the current box is gone but the cargoplace is still open.
// The operator drops back to the box scan without losing the cargoplace.
const BOX_TERMINAL_CODES = [
  'BOX_NOT_FOUND',
  'BOX_NOT_OPEN',
  'BOX_NOT_IN_CARGOPLACE',
]

interface ApiErrorBody {
  error?: {
    code?: string
    message?: string
  }
}

function readApiError(error: unknown): {
  code: string | null
  message: string | null
} {
  if (!isAxiosError(error)) {
    return { code: null, message: null }
  }

  const data = error.response?.data

  // 401 responses are plain text, not the JSON error envelope.
  if (data && typeof data === 'object' && 'error' in data) {
    const body = data as ApiErrorBody
    return {
      code: body.error?.code ?? null,
      message: body.error?.message ?? null,
    }
  }

  return { code: null, message: null }
}

export function getReceivingErrorCode(error: unknown): string | null {
  return readApiError(error).code
}

export function getReceivingErrorMessage(error: unknown): string {
  // The API already returns friendly Russian messages — prefer them.
  const { message } = readApiError(error)
  if (message) {
    return message
  }

  if (isAxiosError(error)) {
    const status = error.response?.status

    if (status === 401) {
      return 'Сессия истекла. Войдите в систему заново.'
    }
    if (status === 403) {
      return 'Недостаточно прав: операция доступна только оператору.'
    }
    if (status !== undefined && status >= 500) {
      return 'Внутренняя ошибка сервера. Повторите попытку позже.'
    }
    if (error.code === 'ERR_NETWORK') {
      return 'Нет соединения с сервером. Проверьте подключение.'
    }
  }

  return 'Не удалось выполнить операцию. Повторите попытку.'
}

export function isCargoplaceTerminalError(error: unknown): boolean {
  const code = getReceivingErrorCode(error)
  return code !== null && CARGOPLACE_TERMINAL_CODES.includes(code)
}

export function isBoxTerminalError(error: unknown): boolean {
  const code = getReceivingErrorCode(error)
  return code !== null && BOX_TERMINAL_CODES.includes(code)
}
