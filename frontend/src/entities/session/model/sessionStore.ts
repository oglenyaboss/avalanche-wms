import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

import type { User } from '@/entities/user/@x/session'
import type { AuthTokens } from '@/shared/api'

import { refreshTokensRequest } from '../api/sessionApi'
import { getUserFromAccessToken } from './token'

type SessionPersistedState = Pick<SessionState, 'accessToken' | 'refreshToken'>

type SessionState = {
  accessToken: string | null
  refreshToken: string | null
  user: User | null
  isAuthenticated: boolean
  logout: () => void
  refreshSession: () => Promise<AuthTokens>
  startSession: (tokens: AuthTokens) => void
}

function createAuthorizedState(tokens: AuthTokens) {
  const user = getUserFromAccessToken(tokens.accessToken)

  return {
    ...tokens,
    user,
    isAuthenticated: Boolean(user),
  }
}

const emptySessionState = {
  accessToken: null,
  refreshToken: null,
  user: null,
  isAuthenticated: false,
}

export const useSessionStore = create<SessionState>()(
  persist(
    (set, get) => ({
      ...emptySessionState,
      logout: () => {
        set(emptySessionState)
      },
      refreshSession: async () => {
        const refreshToken = get().refreshToken

        if (!refreshToken) {
          throw new Error('Refresh token is missing')
        }

        const tokens = await refreshTokensRequest(refreshToken)

        set(createAuthorizedState(tokens))

        return tokens
      },
      startSession: (tokens) => {
        set(createAuthorizedState(tokens))
      },
    }),
    {
      name: 'wms-auth',
      storage: createJSONStorage(() => localStorage),
      partialize: (state) => ({
        accessToken: state.accessToken,
        refreshToken: state.refreshToken,
      }),
      merge: (persistedState, currentState) => {
        const persisted = persistedState as SessionPersistedState

        if (!persisted.accessToken || !persisted.refreshToken) {
          return currentState
        }

        return {
          ...currentState,
          ...createAuthorizedState({
            accessToken: persisted.accessToken,
            refreshToken: persisted.refreshToken,
          }),
        }
      },
    },
  ),
)

