import { useQuery } from '@tanstack/react-query'
import { isAxiosError } from 'axios'

import {
  getProductTimeline,
  getRecentProducts,
  getTxProof,
} from '../api/traceabilityApi'

const COLD_LOAD_RETRIES = 2

// Mirrors analytics: retry the cold-load 401 race (auth interceptor registers in
// an effect after the first query fires) and transient network errors only.
function retryColdLoadRace(failureCount: number, error: unknown): boolean {
  if (failureCount >= COLD_LOAD_RETRIES) return false
  if (isAxiosError(error)) {
    return error.response?.status === 401 || error.code === 'ERR_NETWORK'
  }
  return false
}

export function useRecentProducts() {
  return useQuery({
    queryKey: ['traceability', 'recent'],
    queryFn: () => getRecentProducts(20),
    retry: retryColdLoadRace,
    retryDelay: 400,
  })
}

export function useProductTimeline(key: string | null) {
  return useQuery({
    queryKey: ['traceability', 'timeline', key],
    queryFn: () => getProductTimeline(key as string),
    enabled: key !== null && key !== '',
    retry: retryColdLoadRace,
    retryDelay: 400,
  })
}

// Enabled only once the user clicks a hash. 404 from a not-yet-mined tx never
// happens here (adapter returns found:false at 200); only real errors surface.
export function useTxProof(hash: string | null) {
  return useQuery({
    queryKey: ['traceability', 'txProof', hash],
    queryFn: () => getTxProof(hash as string),
    enabled: hash !== null,
    retry: retryColdLoadRace,
    retryDelay: 400,
  })
}
