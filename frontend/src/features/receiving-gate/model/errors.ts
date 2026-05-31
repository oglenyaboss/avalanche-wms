import { isAxiosError } from 'axios'

// Codes that mean the shipment can no longer be received at the gate. When the
// API returns one of these, the operator must restart from the TTN scan.
const TERMINAL_ERROR_CODES = [
  'SHIPMENT_NOT_IN_PROGRESS',
  'SHIPMENT_NOT_FOUND',
  'SHIPMENT_ALREADY_CLOSED',
]

interface ApiErrorBody {
  error?: {
    code?: string
    message?: string
  }
}

function readApiError(error: unknown): { code: string | null; message: string | null } {
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

export function isTerminalShipmentError(error: unknown): boolean {
  const code = getReceivingErrorCode(error)
  return code !== null && TERMINAL_ERROR_CODES.includes(code)
}
