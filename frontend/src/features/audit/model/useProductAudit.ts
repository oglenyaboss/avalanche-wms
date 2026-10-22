import { useQuery } from '@tanstack/react-query'
import { isAddress, type Address } from 'viem'

import { getProductAudit } from '../api/auditApi'
import { createChainReader } from '../api/chainReader'
import { auditProduct } from './auditProduct'
import type { AuditStage, ProductAudit } from './types'

export type ProductAuditResult = {
  product: ProductAudit
  stages: AuditStage[]
}

// Runs only with complete settings: an empty RPC url or a malformed contract
// address must never turn into a network request.
export function useProductAudit(
  key: string | null,
  rpcUrl: string,
  contractAddress: string,
) {
  return useQuery({
    queryKey: ['audit', key, rpcUrl, contractAddress],
    queryFn: async (): Promise<ProductAuditResult> => {
      const product = await getProductAudit(key as string)
      const stages = await auditProduct(product, createChainReader(rpcUrl, contractAddress as Address))
      return { product, stages }
    },
    enabled: key !== null && key !== '' && rpcUrl !== '' && isAddress(contractAddress),
    retry: false,
  })
}
