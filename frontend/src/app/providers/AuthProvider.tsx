import { type PropsWithChildren } from 'react'

import { configureSessionApi, useSessionStore } from '@/entities/session'
import { routes } from '@/shared/config'

// Register the axios session interceptors at module load — BEFORE any component
// renders and fires its first query. Registering them inside a useEffect (the
// previous approach) runs too late: React flushes children's effects before the
// parent's, so on a cold page load a protected route's initial request races
// ahead of the interceptor and goes out without an Authorization header →
// spurious 401 / forced logout. (analytics worked around this with a 401-retry;
// the other stage pages did not.) Module scope runs exactly once, before React
// renders, and is immune to StrictMode's effect double-invoke. The callbacks read
// the session store lazily, so this is safe to wire before the store rehydrates.
configureSessionApi({
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

export function AuthProvider({ children }: PropsWithChildren) {
  return children
}
