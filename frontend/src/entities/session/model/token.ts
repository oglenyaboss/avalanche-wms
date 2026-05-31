import { jwtDecode } from 'jwt-decode'

import type { User, UserRole } from '@/entities/user/@x/session'

type TokenPayload = {
  user_id: string
  role: UserRole
  type: 'access' | 'refresh'
  exp: number
}

export function getUserFromAccessToken(accessToken: string): User | null {
  try {
    const payload = jwtDecode<Partial<TokenPayload>>(accessToken)

    if (payload.type !== 'access' || !payload.user_id || !payload.role) {
      return null
    }

    return {
      id: payload.user_id,
      role: payload.role,
    }
  } catch {
    return null
  }
}
