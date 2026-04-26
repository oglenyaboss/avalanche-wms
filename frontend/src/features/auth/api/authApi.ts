import {
  mapAuthTokensResponse,
  type AuthTokensResponse,
} from '@/entities/session'
import { publicApiClient } from '@/shared/api'

import type { LoginFormValues } from '../model/schema'
import { getTemporaryDevTokens } from './devLogin'

export async function loginRequest(credentials: LoginFormValues) {
  const temporaryDevTokens = getTemporaryDevTokens(credentials)

  if (temporaryDevTokens) {
    return temporaryDevTokens
  }

  const { data } = await publicApiClient.post<AuthTokensResponse>(
    '/auth/login',
    credentials,
  )

  return mapAuthTokensResponse(data)
}
