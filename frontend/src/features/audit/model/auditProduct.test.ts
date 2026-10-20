// auditProduct against a mocked chain reader built from the golden vectors.
import { describe, expect, it, vi } from 'vitest'
import { bytesToHex, hexToBigInt, hexToBytes, type Hex } from 'viem'

// The golden vectors live in the repo root, outside the @/ alias.
// eslint-disable-next-line no-restricted-imports
import vectorsRaw from '../../../../../testdata/signed-actions/vectors.v1.json?raw'
import { auditProduct } from './auditProduct'
import type { AppliedAction, ChainReader, OnChainAuth, RejectedAction } from '../api/chainReader'
import type { DbAction, ProductAudit } from './types'

type Vectors = {
  actions: {
    actionId: string
    actionType: number
    itemIds: Hex[]
    itemsHash: Hex
    detailsHash: Hex
    challenge: Hex
    name: string
    details: { salt?: Hex; storageBinId?: string; operatorId?: string; occurredAtMs?: number }
    webauthn: {
      authenticatorData: Hex
      clientDataJSONHex: Hex
      challengeIndex: number
      typeIndex: number
      r: Hex
      s: Hex
    }
  }[]
  products: { productId: string; itemId: Hex }[]
  signer: { keyId: Hex; qx: Hex; qy: Hex }
  orgId: string
}

const vectors = JSON.parse(vectorsRaw) as Vectors
const putAway = vectors.actions.find((action) => action.name === 'putAway')!

const TX = ('0x' + 'aa'.repeat(32)) as Hex
const TX_UNKNOWN = ('0x' + 'bb'.repeat(32)) as Hex
const UNKNOWN_ID = '01929c5e-6f00-7000-8000-000000000999'

function dbAction(overrides: Partial<DbAction> = {}): DbAction {
  const details = putAway.details
  return {
    actionId: putAway.actionId,
    operationId: '01929c5e-6f00-7000-8000-000000000777',
    partIndex: 0,
    actionType: putAway.actionType,
    itemIds: vectors.products
      .filter((product) => putAway.itemIds.includes(product.itemId))
      .map((product) => product.productId),
    salt: details.salt!,
    submittedAt: '2025-10-18T10:01:00Z',
    operatorId: details.operatorId!,
    operator: 'Оператор',
    orgId: vectors.orgId,
    orgName: 'Склад №1',
    keyId: vectors.signer.keyId,
    chainStatus: 'COMMITTED',
    rejectReason: null,
    details: {
      storageBinId: details.storageBinId!,
      operatorId: details.operatorId!,
      occurredAtMs: details.occurredAtMs!,
    },
    detailsError: null,
    ...overrides,
  }
}

function dbAudit(action: DbAction): ProductAudit {
  return {
    productId: vectors.products[0].productId,
    itemId: vectors.products[0].itemId,
    actions: [action],
  }
}

function vectorAuth(): OnChainAuth {
  return {
    itemIds: putAway.itemIds.map((id) => hexToBigInt(id)),
    detailsHash: putAway.detailsHash,
    keyId: vectors.signer.keyId,
    actionType: putAway.actionType,
    webauthn: {
      authenticatorData: putAway.webauthn.authenticatorData,
      clientDataJSON: new TextDecoder().decode(hexToBytes(putAway.webauthn.clientDataJSONHex)),
      challengeIndex: putAway.webauthn.challengeIndex,
      typeIndex: putAway.webauthn.typeIndex,
      r: putAway.webauthn.r,
      s: putAway.webauthn.s,
    },
  }
}

function appliedOf(id: string, overrides: Partial<AppliedAction> = {}): AppliedAction {
  return {
    actionId: id,
    keyId: vectors.signer.keyId,
    orgId: vectors.orgId,
    actionType: putAway.actionType,
    detailsHash: putAway.detailsHash,
    itemCount: 3n,
    blockNumber: 42n,
    txHash: TX,
    ...overrides,
  }
}

function mockChain(
  options: {
    chainIds?: string[]
    applied?: AppliedAction[]
    rejected?: RejectedAction[]
    auth?: OnChainAuth | null
  } = {},
): ChainReader {
  const appliedList = options.applied ?? [appliedOf(putAway.actionId)]
  return {
    actionIdsForItem: vi.fn().mockResolvedValue(options.chainIds ?? [putAway.actionId]),
    appliedActions: vi.fn().mockResolvedValue(
      new Map<string, AppliedAction>(appliedList.map((a) => [a.actionId, a] as const)),
    ),
    rejectedActions: vi.fn().mockResolvedValue(
      new Map<string, RejectedAction>((options.rejected ?? []).map((r) => [r.actionId, r] as const)),
    ),
    key: vi.fn().mockResolvedValue({
      qx: vectors.signer.qx,
      qy: vectors.signer.qy,
      orgId: vectors.orgId,
      role: 1,
      active: true,
    }),
    organizationName: vi.fn().mockResolvedValue('Склад №1'),
    actionAuth: vi.fn().mockResolvedValue(options.auth !== undefined ? options.auth : vectorAuth()),
    blockTime: vi.fn().mockResolvedValue(new Date(1760781660000)),
  }
}

describe('auditProduct', () => {
  it('applies consistently: one stage, all checks ok, no problems', async () => {
    const stages = await auditProduct(dbAudit(dbAction()), mockChain())
    expect(stages).toHaveLength(1)
    expect(stages[0].status).toBe('applied')
    expect(stages[0].detailsHash).toBe('ok')
    expect(stages[0].items).toBe('ok')
    expect(stages[0].signer).toBe('ok')
    expect(stages[0].signature).toBe('ok')
    expect(stages[0].problems).toEqual([])
    expect(stages[0].orgId).toBe(vectors.orgId)
    expect(stages[0].orgName).toBe('Склад №1')
    expect(stages[0].blockNumber).toBe(42n)
  })

  it('flags a changed DB storage bin as detailsHash mismatch', async () => {
    const action = dbAction({
      details: { ...dbAction().details!, storageBinId: '01929c5e-6f00-7000-8000-000000000fff' },
    })
    const stages = await auditProduct(dbAudit(action), mockChain())
    expect(stages[0].detailsHash).toBe('mismatch')
    expect(stages[0].problems).toContain('Хеш деталей из БД не совпадает с цепью')
    expect(stages[0].status).toBe('applied')
    expect(stages[0].signature).toBe('ok')
  })

  it('flags missing DB details via detailsError', async () => {
    const action = dbAction({ details: null, detailsError: 'Строка putaways не найдена' })
    const stages = await auditProduct(dbAudit(action), mockChain())
    expect(stages[0].detailsHash).toBe('mismatch')
    expect(stages[0].problems).toContain('Строка putaways не найдена')
  })

  it('marks a chain action unknown to the DB as missingInDb, ordered by block', async () => {
    const unknown = appliedOf(UNKNOWN_ID, { blockNumber: 43n, txHash: TX_UNKNOWN })
    const chain = mockChain({
      chainIds: [putAway.actionId, UNKNOWN_ID],
      applied: [appliedOf(putAway.actionId), unknown],
    })
    const stages = await auditProduct(dbAudit(dbAction()), chain)
    expect(stages).toHaveLength(2)
    expect(stages[0].actionId).toBe(putAway.actionId)
    expect(stages[1].status).toBe('missingInDb')
    expect(stages[1].detailsHash).toBe('skipped')
    expect(stages[1].problems).toEqual(['Действие есть в цепи, но нет в БД'])
    expect(stages[1].orgId).toBe(vectors.orgId)
    expect(stages[1].blockNumber).toBe(43n)
  })

  it('marks a committed DB action absent from the chain as missingOnChain', async () => {
    const stages = await auditProduct(dbAudit(dbAction()), mockChain({ chainIds: [], applied: [] }))
    expect(stages[0].status).toBe('missingOnChain')
    expect(stages[0].problems).toEqual(['Действие есть в БД, но нет в цепи'])
  })

  it('marks a pending DB action as pending without problems', async () => {
    const action = dbAction({ chainStatus: 'PENDING' })
    const stages = await auditProduct(dbAudit(action), mockChain({ chainIds: [], applied: [] }))
    expect(stages[0].status).toBe('pending')
    expect(stages[0].problems).toEqual([])
  })

  it('flags a flipped signature r as signature mismatch', async () => {
    const auth = vectorAuth()
    const r = hexToBytes(auth.webauthn.r)
    r[0] ^= 1
    const stages = await auditProduct(
      dbAudit(dbAction()),
      mockChain({ auth: { ...auth, webauthn: { ...auth.webauthn, r: bytesToHex(r) } } }),
    )
    expect(stages[0].signature).toBe('mismatch')
    expect(stages[0].items).toBe('ok')
    expect(stages[0].status).toBe('applied')
  })
})
