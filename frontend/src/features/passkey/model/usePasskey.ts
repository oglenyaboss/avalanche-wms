import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'

import {
  beginRegistration,
  finishRegistration,
  getAdminKeyExport,
  getMyPasskey,
  getOrganizationKeys,
} from '../api/passkeyApi'
import { createPasskeyCredential, toCreationOptions } from './webauthn'
import { downloadAdminKey } from './exportFile'
import { passkeyErrorText } from './errors'

const COLD_LOAD_RETRIES = 2

// Mirrors traceability: retry the cold-load 401 race and transient network errors only.
function retryColdLoadRace(failureCount: number, error: unknown): boolean {
  if (failureCount >= COLD_LOAD_RETRIES) return false
  if (isAxiosError(error)) {
    return error.response?.status === 401 || error.code === 'ERR_NETWORK'
  }
  return false
}

export function useMyPasskey() {
  return useQuery({
    queryKey: ['passkey', 'me'],
    queryFn: getMyPasskey,
    retry: retryColdLoadRace,
    retryDelay: 400,
  })
}

export function useOrganizationKeys() {
  return useQuery({
    queryKey: ['passkey', 'organization'],
    queryFn: getOrganizationKeys,
    retry: retryColdLoadRace,
    retryDelay: 400,
  })
}

export function useRegisterPasskey() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: ['passkey', 'register'],
    mutationFn: async () => {
      const options = await beginRegistration()
      const credential = await createPasskeyCredential(toCreationOptions(options))
      return finishRegistration(credential)
    },
    retry: false,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['passkey'] }),
  })
}

export function useAdminKeyDownload() {
  const mutation = useMutation({
    mutationKey: ['passkey', 'adminKeyExport'],
    mutationFn: getAdminKeyExport,
    retry: false,
    onSuccess: downloadAdminKey,
  })

  const errorText = mutation.error
    ? isAxiosError(mutation.error) && mutation.error.response?.status === 404
      ? 'Сначала создайте свой ключ на странице «Мой ключ»'
      : passkeyErrorText(mutation.error)
    : null

  return {
    download: mutation.mutate,
    isPending: mutation.isPending,
    errorText,
  }
}
