import axios from 'axios'

import { env } from '../config/env'

export const apiClient = axios.create({
  baseURL: env.apiBaseUrl,
  withCredentials: true,
})

export const publicApiClient = axios.create({
  baseURL: env.apiBaseUrl,
  withCredentials: true,
})

export type AuthTokens = {
  accessToken: string
  refreshToken: string
}

