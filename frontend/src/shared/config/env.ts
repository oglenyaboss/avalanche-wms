export const env = {
  apiBaseUrl: import.meta.env.VITE_API_BASE_URL ?? '/api',
  auditRpcUrl: import.meta.env.VITE_AUDIT_RPC_URL ?? '',
  auditContractAddress: import.meta.env.VITE_AUDIT_CONTRACT_ADDR ?? '',
} as const
