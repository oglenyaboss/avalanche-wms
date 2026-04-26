import { useEffect, type PropsWithChildren } from 'react'

import { configureSessionApi, useSessionStore } from '@/entities/session'
import { routes } from '@/shared/config'

export function AuthProvider({ children }: PropsWithChildren) {
  useEffect(() => {
    return configureSessionApi({
      clearSession: () => useSessionStore.getState().logout(),
      getAccessToken: () => useSessionStore.getState().accessToken,
      hasRefreshToken: () => Boolean(useSessionStore.getState().refreshToken),
      redirectToLogin: () => {
        if (window.location.pathname !== routes.login) {
          window.location.assign(routes.login)
        }
      },
      refreshSession: async () => {
        const tokens = await useSessionStore.getState().refreshSession()

        return tokens.accessToken
      },
    })
  }, [])

  return children
}

