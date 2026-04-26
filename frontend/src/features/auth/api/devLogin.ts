import type { AuthTokens } from '@/shared/api'

import type { LoginFormValues } from '../model/schema'

const devCredentials = {
  username: 'root@gmail.com',
  password: 'root12345',
}

const devUser = {
  id: 'dev-root',
  role: 'ADMIN',
} as const

function encodeBase64Url(payload: unknown) {
  return btoa(JSON.stringify(payload))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/g, '')
}

function createDevJwt(type: 'access' | 'refresh') {
  const header = encodeBase64Url({
    alg: 'none',
    typ: 'JWT',
  })
  const payload = encodeBase64Url({
    user_id: devUser.id,
    role: devUser.role,
    type,
    exp: Math.floor(Date.now() / 1000) + 60 * 60 * 24 * 7,
  })

  return `${header}.${payload}.temporary-dev-signature`
}

export function getTemporaryDevTokens(
  credentials: LoginFormValues,
): AuthTokens | null {
  if (!import.meta.env.DEV) {
    return null
  }

  if (
    credentials.username !== devCredentials.username ||
    credentials.password !== devCredentials.password
  ) {
    return null
  }

  return {
    accessToken: createDevJwt('access'),
    refreshToken: createDevJwt('refresh'),
  }
}
