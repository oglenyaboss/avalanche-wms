// Auditor settings for the direct chain read, shared across the audit page:
// localStorage-backed with env fallbacks; every storage access is guarded so
// private browsing modes keep working.
import { create } from 'zustand'

import { env } from '@/shared/config'

const RPC_URL_KEY = 'audit.rpcUrl'
const CONTRACT_ADDRESS_KEY = 'audit.contractAddress'

type AuditSettingsState = {
  rpcUrl: string
  contractAddress: string
  setRpcUrl: (value: string) => void
  setContractAddress: (value: string) => void
}

function readStored(key: string): string {
  try {
    return localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function writeStored(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Настройка остаётся только в памяти до перезагрузки.
  }
}

export const useAuditSettings = create<AuditSettingsState>()((set) => ({
  rpcUrl: readStored(RPC_URL_KEY) || env.auditRpcUrl,
  contractAddress: readStored(CONTRACT_ADDRESS_KEY) || env.auditContractAddress,
  setRpcUrl: (value) => {
    writeStored(RPC_URL_KEY, value)
    set({ rpcUrl: value })
  },
  setContractAddress: (value) => {
    writeStored(CONTRACT_ADDRESS_KEY, value)
    set({ contractAddress: value })
  },
}))
