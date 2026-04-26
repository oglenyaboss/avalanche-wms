import type {
  AxiosError,
  AxiosRequestHeaders,
  InternalAxiosRequestConfig,
} from 'axios'

import { apiClient } from '@/shared/api'

type ConfigureSessionApiCallbacks = {
  clearSession: () => void
  getAccessToken: () => string | null
  hasRefreshToken: () => boolean
  redirectToLogin: () => void
  refreshSession: () => Promise<string>
}

type RetryableRequestConfig = InternalAxiosRequestConfig & {
  _retry?: boolean
}

function setAuthorizationHeader(
  config: InternalAxiosRequestConfig,
  accessToken: string,
) {
  if (typeof config.headers.set === 'function') {
    config.headers.set('Authorization', `Bearer ${accessToken}`)
    return
  }

  config.headers = {
    ...config.headers,
    Authorization: `Bearer ${accessToken}`,
  } as AxiosRequestHeaders
}

export function configureSessionApi(callbacks: ConfigureSessionApiCallbacks) {
  let refreshPromise: Promise<string> | null = null

  const requestInterceptorId = apiClient.interceptors.request.use((config) => {
    const accessToken = callbacks.getAccessToken()

    if (accessToken) {
      setAuthorizationHeader(config, accessToken)
    }

    return config
  })

  const responseInterceptorId = apiClient.interceptors.response.use(
    (response) => response,
    async (error: AxiosError) => {
      const status = error.response?.status
      const originalRequest = error.config as RetryableRequestConfig | undefined

      if (status !== 401 || !originalRequest || originalRequest._retry) {
        throw error
      }

      if (!callbacks.hasRefreshToken()) {
        callbacks.clearSession()
        callbacks.redirectToLogin()
        throw error
      }

      originalRequest._retry = true

      try {
        refreshPromise ??= callbacks.refreshSession().finally(() => {
          refreshPromise = null
        })

        const accessToken = await refreshPromise

        setAuthorizationHeader(originalRequest, accessToken)

        return apiClient(originalRequest)
      } catch {
        callbacks.clearSession()
        callbacks.redirectToLogin()
        throw error
      }
    },
  )

  return () => {
    apiClient.interceptors.request.eject(requestInterceptorId)
    apiClient.interceptors.response.eject(responseInterceptorId)
  }
}

