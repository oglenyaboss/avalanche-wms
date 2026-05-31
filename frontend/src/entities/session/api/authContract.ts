import type { AuthTokens } from '@/shared/api'

export type AuthTokensResponse = {
  access_token: string
  refresh_token: string
}

export function mapAuthTokensResponse(response: AuthTokensResponse): AuthTokens {
  return {
    accessToken: response.access_token,
    refreshToken: response.refresh_token,
  }
}

