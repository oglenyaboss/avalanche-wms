import { useQuery } from '@tanstack/react-query'

import { getOperationStatus } from '../api/signedActionApi'

// Polls the chain status of a signed operation every 2s while at least one
// part is still SENT; stops once every part reached a terminal state.
export function useOperationStatus(operationId: string | null) {
  return useQuery({
    queryKey: ['signed-action', 'operation', operationId],
    queryFn: () => getOperationStatus(operationId as string),
    enabled: operationId !== null,
    refetchInterval: (query) =>
      query.state.data?.parts.some((part) => part.state === 'SENT') ? 2000 : false,
  })
}
