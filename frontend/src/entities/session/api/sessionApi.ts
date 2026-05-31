import { publicApiClient } from '@/shared/api'

import { mapAuthTokensResponse, type AuthTokensResponse } from './authContract'

export async function refreshTokensRequest(refreshToken: string) {
  const { data } = await publicApiClient.post<AuthTokensResponse>(
    '/auth/refresh',
    {
      refresh_token: refreshToken,
    },
  )

  return mapAuthTokensResponse(data)
}

