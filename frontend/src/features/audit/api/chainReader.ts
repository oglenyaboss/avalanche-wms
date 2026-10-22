// Direct chain reads for the auditor: events, registry key and applyActions
// calldata of a transaction (ADR-0001 §8).
import {
  createPublicClient,
  decodeFunctionData,
  http,
  isAddressEqual,
  type Address,
  type Hex,
  type Transport,
} from 'viem'

import { uuidToBytes16, type WebAuthnTuple } from '@/entities/signed-action'

import {
  actionAppliedEvent,
  actionRejectedEvent,
  itemMovedEvent,
  organizationRegisteredEvent,
  registryAbi,
  wmsAbi,
} from './chainAbi'

export type AppliedAction = {
  actionId: string
  keyId: Hex
  orgId: string
  actionType: number
  detailsHash: Hex
  itemCount: bigint
  blockNumber: bigint
  txHash: Hex
}

export type RejectedAction = {
  actionId: string
  reason: number
  txHash: Hex
}

export type RegistryKey = {
  qx: Hex
  qy: Hex
  orgId: string
  role: number
  active: boolean
}

export type OnChainAuth = {
  itemIds: bigint[]
  detailsHash: Hex
  keyId: Hex
  actionType: number
  webauthn: WebAuthnTuple
}

export type ChainReader = {
  actionIdsForItem(itemId: bigint): Promise<string[]>
  appliedActions(actionIds: string[]): Promise<Map<string, AppliedAction>>
  rejectedActions(actionIds: string[]): Promise<Map<string, RejectedAction>>
  key(keyId: Hex): Promise<RegistryKey>
  organizationName(orgId: string): Promise<string | null>
  actionAuth(txHash: Hex, actionId: string): Promise<OnChainAuth | null>
  blockTime(blockNumber: bigint): Promise<Date>
}

// bytes16 → dashed lowercase uuid; accepts both the decoded 16-byte value and
// the 32-byte topic form.
export function bytes16ToUuid(value: Hex): string {
  const hex = value.replace(/^0x/i, '').slice(0, 32).padEnd(32, '0').toLowerCase()
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`
}

export function createChainReader(rpcUrl: string, contract: Address, transport: Transport = http(rpcUrl)): ChainReader {
  const client = createPublicClient({ transport })
  const blockTimes = new Map<bigint, Date>()
  let registryAddress: Address | null = null

  const registryAddr = async (): Promise<Address> => {
    registryAddress ??= await client.readContract({
      address: contract,
      abi: wmsAbi,
      functionName: 'registry',
    })
    return registryAddress
  }

  const actionIdsForItem = async (itemId: bigint): Promise<string[]> => {
    const logs = await client.getLogs({
      address: contract,
      event: itemMovedEvent,
      args: { itemId },
      fromBlock: 0n,
    })
    const ids: string[] = []
    for (const log of logs) {
      if (log.args.actionId === undefined) continue
      const id = bytes16ToUuid(log.args.actionId)
      if (!ids.includes(id)) ids.push(id)
    }
    return ids
  }

  const appliedActions = async (actionIds: string[]): Promise<Map<string, AppliedAction>> => {
    const out = new Map<string, AppliedAction>()
    if (actionIds.length === 0) return out
    const logs = await client.getLogs({
      address: contract,
      event: actionAppliedEvent,
      args: { actionId: actionIds.map(uuidToBytes16) },
      fromBlock: 0n,
    })
    for (const log of logs) {
      const { actionId, keyId, orgId, actionType, detailsHash, itemCount } = log.args
      if (
        actionId === undefined ||
        keyId === undefined ||
        orgId === undefined ||
        actionType === undefined ||
        detailsHash === undefined ||
        itemCount === undefined
      )
        continue
      const id = bytes16ToUuid(actionId)
      out.set(id, {
        actionId: id,
        keyId,
        orgId: bytes16ToUuid(orgId),
        actionType,
        detailsHash,
        itemCount,
        blockNumber: log.blockNumber ?? 0n,
        txHash: log.transactionHash ?? '0x',
      })
    }
    return out
  }

  const rejectedActions = async (actionIds: string[]): Promise<Map<string, RejectedAction>> => {
    const out = new Map<string, RejectedAction>()
    if (actionIds.length === 0) return out
    const logs = await client.getLogs({
      address: contract,
      event: actionRejectedEvent,
      args: { actionId: actionIds.map(uuidToBytes16) },
      fromBlock: 0n,
    })
    for (const log of logs) {
      const { actionId, reason } = log.args
      if (actionId === undefined || reason === undefined) continue
      const id = bytes16ToUuid(actionId)
      out.set(id, { actionId: id, reason, txHash: log.transactionHash ?? '0x' })
    }
    return out
  }

  const key = async (keyId: Hex): Promise<RegistryKey> => {
    const registry = await client.readContract({
      address: await registryAddr(),
      abi: registryAbi,
      functionName: 'getKey',
      args: [keyId],
    })
    return { qx: registry.qx, qy: registry.qy, orgId: bytes16ToUuid(registry.orgId), role: registry.role, active: registry.active }
  }

  const organizationName = async (orgId: string): Promise<string | null> => {
    const logs = await client.getLogs({
      address: await registryAddr(),
      event: organizationRegisteredEvent,
      args: { orgId: uuidToBytes16(orgId) },
      fromBlock: 0n,
    })
    return logs.at(-1)?.args.name ?? null
  }

  const actionAuth = async (txHash: Hex, actionId: string): Promise<OnChainAuth | null> => {
    try {
      const tx = await client.getTransaction({ hash: txHash })
      if (!tx.to || !isAddressEqual(tx.to, contract)) return null
      const decoded = decodeFunctionData({ abi: wmsAbi, data: tx.input })
      if (decoded.functionName !== 'applyActions') return null
      const [actions] = decoded.args
      const entry = actions.find((a) => bytes16ToUuid(a.actionId) === actionId.toLowerCase())
      if (!entry) return null
      return {
        itemIds: [...entry.itemIds],
        detailsHash: entry.detailsHash,
        keyId: entry.keyId,
        actionType: entry.actionType,
        webauthn: {
          authenticatorData: entry.auth.authenticatorData,
          clientDataJSON: entry.auth.clientDataJSON,
          challengeIndex: Number(entry.auth.challengeIndex),
          typeIndex: Number(entry.auth.typeIndex),
          r: entry.auth.r,
          s: entry.auth.s,
        },
      }
    } catch {
      return null
    }
  }

  const blockTime = async (blockNumber: bigint): Promise<Date> => {
    const cached = blockTimes.get(blockNumber)
    if (cached) return cached
    const block = await client.getBlock({ blockNumber })
    const time = new Date(Number(block.timestamp) * 1000)
    blockTimes.set(blockNumber, time)
    return time
  }

  return { actionIdsForItem, appliedActions, rejectedActions, key, organizationName, actionAuth, blockTime }
}
