import { isAxiosError } from 'axios'

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

// Operator-facing texts for the signed-ship errors, used when the envelope
// arrives without a message; the backend normally supplies these itself.
const SIGNING_ERROR_TEXTS: Record<string, string> = {
  PICKING_NOT_CONFIRMED:
    'Сборка части товаров ещё не подтверждена в цепи. Повторите отгрузку чуть позже.',
  DRAFT_STALE: 'Состав буфера изменился, повторите отгрузку.',
}

export function getShippingErrorMessage(error: unknown): string {
  // The API already returns friendly Russian messages — prefer them. These cover
  // the shipping error codes: BIN_NOT_FOUND, BIN_NOT_SHIPPING_BUFFER,
  // DISPATCH_NOT_FOUND, DISPATCH_ALREADY_DEPARTED, DISPATCH_CANCELLED,
  // DESTINATION_MISMATCH, DISPATCH_NOT_AT_GATE, BUFFER_EMPTY,
  // PRODUCT_NOT_IN_BUFFER, SIGNER_KEY_NOT_ACTIVE, PICKING_NOT_CONFIRMED,
  // DRAFT_STALE, OPERATION_ALREADY_SUBMITTED, INVALID_ASSERTION, INVALID_REQUEST.
  const { code, message } = readApiError(error)
  if (message) {
    return message
  }

  if (code && SIGNING_ERROR_TEXTS[code]) {
    return SIGNING_ERROR_TEXTS[code]
  }

  if (isAxiosError(error)) {
    const status = error.response?.status

    if (status === 401) {
      return 'Сессия истекла. Войдите в систему заново.'
    }
    if (status === 403) {
      return 'Недостаточно прав: отгрузка доступна только оператору.'
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
