import { isAxiosError } from 'axios'

interface ApiErrorBody {
  error?: {
    code?: string
    message?: string
  }
}

function readApiError(error: unknown): { message: string | null } {
  if (!isAxiosError(error)) {
    return { message: null }
  }
  const data = error.response?.data
  if (data && typeof data === 'object' && 'error' in data) {
    const body = data as ApiErrorBody
    return { message: body.error?.message ?? null }
  }
  return { message: null }
}

// Mirrors the shipping/assembly error narrowing: prefer the API's Russian
// message, then fall back by HTTP status, then a generic message.
export function getAnalyticsErrorMessage(error: unknown): string {
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
      return 'Недостаточно прав: аналитика доступна оператору или администратору.'
    }
    if (status !== undefined && status >= 500) {
      return 'Внутренняя ошибка сервера. Повторите попытку позже.'
    }
    if (error.code === 'ERR_NETWORK') {
      return 'Нет соединения с сервером. Проверьте подключение.'
    }
  }

  return 'Не удалось загрузить данные аналитики. Повторите попытку.'
}
