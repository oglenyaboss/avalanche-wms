import { useCallback, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { isAxiosError } from 'axios'

import { getOnchain, getSummary, getThroughput } from '../api/analyticsApi'

// The onchain hero polls so confirmations surface without a manual refresh.
const ONCHAIN_REFETCH_MS = 30_000
const ONCHAIN_STALE_MS = 10_000
const COLD_LOAD_RETRIES = 2

export type ThroughputRange = 7 | 14 | 30

// On a cold page load the axios auth interceptor is registered in an effect that
// runs *after* these queries first fire, so the initial request can go out with
// no Authorization header → 401. Retry that specific race (and transient network
// errors) so the dashboard self-heals, but never retry an authorization (403) or
// other 4xx/5xx, which are real and should surface immediately.
function retryColdLoadRace(failureCount: number, error: unknown): boolean {
  if (failureCount >= COLD_LOAD_RETRIES) return false
  if (isAxiosError(error)) {
    return error.response?.status === 401 || error.code === 'ERR_NETWORK'
  }
  return false
}

export function useAnalytics() {
  const [throughputDays, setThroughputDays] = useState<ThroughputRange>(14)

  const summaryQuery = useQuery({
    queryKey: ['analytics', 'summary'],
    queryFn: getSummary,
    retry: retryColdLoadRace,
    retryDelay: 400,
  })

  const onchainQuery = useQuery({
    queryKey: ['analytics', 'onchain'],
    queryFn: getOnchain,
    retry: retryColdLoadRace,
    retryDelay: 400,
    staleTime: ONCHAIN_STALE_MS,
    refetchInterval: ONCHAIN_REFETCH_MS,
  })

  const throughputQuery = useQuery({
    queryKey: ['analytics', 'throughput', throughputDays],
    queryFn: () => getThroughput(throughputDays),
    retry: retryColdLoadRace,
    retryDelay: 400,
  })

  const refetchAll = useCallback(() => {
    void summaryQuery.refetch()
    void onchainQuery.refetch()
    void throughputQuery.refetch()
  }, [summaryQuery, onchainQuery, throughputQuery])

  // The freshest successful fetch across the three queries drives the "updated
  // at" stamp. 0 means nothing has loaded yet.
  const lastUpdatedMs = Math.max(
    summaryQuery.dataUpdatedAt,
    onchainQuery.dataUpdatedAt,
    throughputQuery.dataUpdatedAt,
  )

  return {
    summary: summaryQuery.data,
    onchain: onchainQuery.data,
    throughput: throughputQuery.data,

    isLoadingSummary: summaryQuery.isPending,
    isLoadingOnchain: onchainQuery.isPending,
    isLoadingThroughput: throughputQuery.isPending,

    summaryError: summaryQuery.error,
    onchainError: onchainQuery.error,
    throughputError: throughputQuery.error,

    isFetching:
      summaryQuery.isFetching ||
      onchainQuery.isFetching ||
      throughputQuery.isFetching,
    lastUpdatedMs: lastUpdatedMs > 0 ? lastUpdatedMs : null,

    throughputDays,
    setThroughputDays,
    refetchAll,
  }
}
