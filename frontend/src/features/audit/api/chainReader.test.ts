// createChainReader against a fake JSON-RPC node: it must read events and
// transactions from the selected WMS contract address only.
import { describe, expect, it } from 'vitest'
import {
  custom,
  encodeAbiParameters,
  encodeEventTopics,
  encodeFunctionData,
  toHex,
  type Address,
  type Hex,
} from 'viem'

import { uuidToBytes16 } from '@/entities/signed-action'

import {
  actionAppliedEvent,
  actionRejectedEvent,
  itemMovedEvent,
  wmsAbi,
} from './chainAbi'
import { createChainReader } from './chainReader'

const RPC_URL = 'http://localhost:8545'
const CONTRACT = ('0x' + 'aa'.repeat(20)) as Address
// Same address as CONTRACT in a different casing; isAddressEqual must accept it.
const CONTRACT_MIXED = ('0x' + 'aA'.repeat(20)) as Hex
const OTHER = ('0x' + 'bb'.repeat(20)) as Address

const X = '01929c5e-6f00-7000-8000-000000000001'
const Y = '01929c5e-6f00-7000-8000-000000000002'
const Z = '01929c5e-6f00-7000-8000-000000000003'
const ORG = uuidToBytes16('01929c5e-6f00-7000-8000-000000000010')

const KEY_ID = ('0x' + '33'.repeat(32)) as Hex
const HASH_OURS = ('0x' + '11'.repeat(32)) as Hex
const HASH_FOREIGN = ('0x' + '99'.repeat(32)) as Hex
const TX_HASH = ('0x' + '77'.repeat(32)) as Hex

type RpcLog = {
  address: Address
  topics: Hex[]
  data: Hex
  blockNumber: Hex
  transactionHash: Hex
  logIndex: Hex
  blockHash: Hex
  transactionIndex: Hex
  removed: boolean
}

let logIndex = 0

function log(input: { address: Address, topics: Hex[], data: Hex }): RpcLog {
  const entry: RpcLog = {
    address: input.address,
    topics: input.topics,
    data: input.data,
    blockNumber: toHex(1n),
    transactionHash: TX_HASH,
    logIndex: toHex(logIndex++),
    blockHash: ('0x' + 'cd'.repeat(32)) as Hex,
    transactionIndex: '0x0',
    removed: false,
  }
  return entry
}

function appliedLog(from: Address, actionId: string, detailsHash: Hex): RpcLog {
  return log({
    address: from,
    topics: encodeEventTopics({
      abi: [actionAppliedEvent],
      eventName: 'ActionApplied',
      args: { actionId: uuidToBytes16(actionId), keyId: KEY_ID, orgId: ORG },
    }) as Hex[],
    data: encodeAbiParameters(
      [{ type: 'uint8' }, { type: 'bytes32' }, { type: 'uint256' }],
      [1, detailsHash, 3n],
    ),
  })
}

function itemMovedLog(from: Address, itemId: bigint, actionId: string): RpcLog {
  return log({
    address: from,
    topics: encodeEventTopics({
      abi: [itemMovedEvent],
      eventName: 'ItemMoved',
      args: { itemId, actionId: uuidToBytes16(actionId) },
    }) as Hex[],
    data: '0x',
  })
}

function rejectedLog(from: Address, actionId: string): RpcLog {
  return log({
    address: from,
    topics: encodeEventTopics({
      abi: [actionRejectedEvent],
      eventName: 'ActionRejected',
      args: { actionId: uuidToBytes16(actionId) },
    }) as Hex[],
    data: encodeAbiParameters([{ type: 'uint8' }, { type: 'uint256' }], [1, 0n]),
  })
}

function actionEntry(actionId: string) {
  return {
    actionId: uuidToBytes16(actionId),
    actionType: 2,
    itemIds: [7n, 8n],
    detailsHash: HASH_OURS,
    keyId: KEY_ID,
    auth: {
      r: ('0x' + '44'.repeat(32)) as Hex,
      s: ('0x' + '55'.repeat(32)) as Hex,
      challengeIndex: 5n,
      typeIndex: 0n,
      authenticatorData: ('0x' + '66'.repeat(32)) as Hex,
      clientDataJSON: '{"challenge":"01929c5e"}',
    },
  }
}

function rpcTx(input: { to: Hex | null, calldata: Hex }): Record<string, unknown> {
  return {
    hash: TX_HASH,
    to: input.to,
    from: OTHER,
    input: input.calldata,
    blockNumber: toHex(2n),
    blockHash: ('0x' + 'cd'.repeat(32)) as Hex,
    transactionIndex: '0x0',
    nonce: '0x0',
    gas: toHex(100_000n),
    gasPrice: toHex(1n),
    value: '0x0',
    type: '0x0',
    v: '0x25',
    r: ('0x' + '44'.repeat(32)) as Hex,
    s: ('0x' + '55'.repeat(32)) as Hex,
    chainId: toHex(4321n),
  }
}

// Filters like a real node: by address only when the filter carries one.
function matchesTopics(
  filter: readonly (Hex | readonly Hex[] | null)[] | undefined,
  logTopics: readonly Hex[],
): boolean {
  if (!filter) return true
  return filter.every((expected, i) => {
    if (expected == null) return true
    const actual = logTopics[i]
    if (actual === undefined) return false
    const candidates = Array.isArray(expected) ? expected : [expected]
    return candidates.some((candidate) => candidate.toLowerCase() === actual.toLowerCase())
  })
}

function fakeNode(options: { logs: RpcLog[], txs?: Record<string, unknown> }) {
  return custom({
    request: async ({ method, params }: { method: string, params?: unknown[] | object }) => {
      switch (method) {
        case 'eth_getLogs': {
          const filter = (Array.isArray(params) ? params[0] : undefined) as
            | { address?: Hex, topics?: (Hex | Hex[] | null)[] }
            | undefined
          const address = typeof filter?.address === 'string' ? filter.address.toLowerCase() : undefined
          return options.logs.filter((entry) => {
            if (address !== undefined && entry.address.toLowerCase() !== address) return false
            return matchesTopics(filter?.topics, entry.topics)
          })
        }
        case 'eth_getTransactionByHash': {
          const hash = Array.isArray(params) ? (params[0] as string) : undefined
          return (hash && options.txs?.[hash]) ?? null
        }
        default:
          throw new Error(`fake node: unexpected method ${method}`)
      }
    },
  })
}

function reader(options: { logs: RpcLog[], txs?: Record<string, unknown> }) {
  return createChainReader(RPC_URL, CONTRACT, fakeNode(options))
}

describe('createChainReader contract filtering', () => {
  it('appliedActions keeps the ActionApplied emitted by the contract', async () => {
    const chain = reader({
      logs: [appliedLog(CONTRACT, X, HASH_OURS), appliedLog(OTHER, X, HASH_FOREIGN)],
    })
    const out = await chain.appliedActions([X])
    expect(out.size).toBe(1)
    expect(out.get(X)?.detailsHash).toBe(HASH_OURS)
  })

  it('appliedActions ignores ActionApplied from another contract', async () => {
    const chain = reader({ logs: [appliedLog(OTHER, Y, HASH_FOREIGN)] })
    const out = await chain.appliedActions([Y])
    expect(out.size).toBe(0)
  })

  it('actionIdsForItem keeps ItemMoved emitted by the contract', async () => {
    const chain = reader({
      logs: [itemMovedLog(OTHER, 7n, Z), itemMovedLog(CONTRACT, 7n, X)],
    })
    const ids = await chain.actionIdsForItem(7n)
    expect(ids).toEqual([X])
  })

  it('rejectedActions ignores ActionRejected from another contract', async () => {
    const chain = reader({ logs: [rejectedLog(OTHER, Y)] })
    const out = await chain.rejectedActions([Y])
    expect(out.size).toBe(0)
  })

  it('actionAuth gives no auth for a tx addressed to another contract', async () => {
    const entry = actionEntry(X)
    const chain = reader({
      logs: [],
      txs: { [TX_HASH]: rpcTx({ to: OTHER, calldata: encodeFunctionData({ abi: wmsAbi, functionName: 'applyActions', args: [[entry]] }) }) },
    })
    const auth = await chain.actionAuth(TX_HASH, X)
    expect(auth).toBeNull()
  })

  it('actionAuth decodes a tx addressed to the contract in another casing', async () => {
    const entry = actionEntry(X)
    const chain = reader({
      logs: [],
      txs: { [TX_HASH]: rpcTx({ to: CONTRACT_MIXED, calldata: encodeFunctionData({ abi: wmsAbi, functionName: 'applyActions', args: [[entry]] }) }) },
    })
    const auth = await chain.actionAuth(TX_HASH, X)
    expect(auth).not.toBeNull()
    expect(auth?.itemIds).toEqual([7n, 8n])
    expect(auth?.detailsHash).toBe(HASH_OURS)
  })
})
