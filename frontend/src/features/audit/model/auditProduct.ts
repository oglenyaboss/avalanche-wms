// Per-stage audit of one product: the DB record from the backend is compared
// against the chain read directly from an RPC node (ADR-0001 §8).
import { challenge, detailsHash, itemId, itemsHash, verifyWebAuthn } from '@/entities/signed-action'

import type { AppliedAction, ChainReader, RejectedAction } from '../api/chainReader'
import type { AuditStage, DbAction, ProductAudit } from './types'

type Entry = { stage: AuditStage; blockNumber: bigint | null; dbIndex: number }

const DETAILS_ERROR_TEXT: Record<string, string> = {
  DOMAIN_ROW_MISSING: 'В БД нет строки этапа для товара этого действия',
  DOMAIN_INCONSISTENT: 'Детали действия в БД расходятся между товарами',
}

export async function auditProduct(db: ProductAudit, chain: ChainReader): Promise<AuditStage[]> {
  const chainIds = await chain.actionIdsForItem(BigInt(db.itemId))
  const ids = [...new Set([...chainIds, ...db.actions.map((a) => a.actionId)])]
  const applied = await chain.appliedActions(ids)
  const rejected = await chain.rejectedActions(ids)

  const entries: Entry[] = []
  for (const id of ids) {
    const dbIndex = db.actions.findIndex((a) => a.actionId === id)
    const dbAction = dbIndex >= 0 ? db.actions[dbIndex] : null
    const chainAction = applied.get(id) ?? null

    if (chainAction === null) {
      if (dbAction === null) continue
      entries.push(dbOnlyStage(id, dbAction, rejected.get(id) ?? null, dbIndex))
      continue
    }

    if (dbAction === null) {
      entries.push({
        stage: {
          actionId: id,
          actionType: chainAction.actionType,
          status: 'missingInDb',
          operator: null,
          operatorId: null,
          orgId: chainAction.orgId,
          orgName: await chain.organizationName(chainAction.orgId),
          blockNumber: chainAction.blockNumber,
          blockTime: await chain.blockTime(chainAction.blockNumber),
          txHash: chainAction.txHash,
          rejectReason: null,
          detailsHash: 'skipped',
          items: 'skipped',
          signer: 'skipped',
          signature: 'skipped',
          problems: ['Действие есть в цепи, но нет в БД'],
        },
        blockNumber: chainAction.blockNumber,
        dbIndex,
      })
      continue
    }

    entries.push(await appliedStage(id, dbAction, chainAction, chain, dbIndex))
  }

  return entries
    .sort((x, y) => {
      if (x.blockNumber !== null && y.blockNumber !== null) {
        if (x.blockNumber !== y.blockNumber) return x.blockNumber < y.blockNumber ? -1 : 1
        return x.dbIndex - y.dbIndex
      }
      if (x.blockNumber !== null) return -1
      if (y.blockNumber !== null) return 1
      return x.dbIndex - y.dbIndex
    })
    .map((entry) => entry.stage)
}

// DB action with no applied log on chain: rejected, pending or missing.
function dbOnlyStage(
  id: string,
  action: DbAction,
  rejection: RejectedAction | null,
  dbIndex: number,
): Entry {
  if (rejection !== null) {
    return {
      stage: {
        actionId: id,
        actionType: action.actionType,
        status: 'rejected',
        operator: action.operator,
        operatorId: action.operatorId,
        orgId: action.orgId,
        orgName: action.orgName,
        blockNumber: null,
        blockTime: null,
        txHash: rejection.txHash,
        rejectReason: rejection.reason,
        detailsHash: 'skipped',
        items: 'skipped',
        signer: 'skipped',
        signature: 'skipped',
        problems: [],
      },
      blockNumber: null,
      dbIndex,
    }
  }

  const pending = action.chainStatus === 'PENDING' || action.chainStatus === 'SENT'
  return {
    stage: {
      actionId: id,
      actionType: action.actionType,
      status: pending ? 'pending' : 'missingOnChain',
      operator: action.operator,
      operatorId: action.operatorId,
      orgId: action.orgId,
      orgName: action.orgName,
      blockNumber: null,
      blockTime: null,
      txHash: null,
      rejectReason: action.rejectReason,
      detailsHash: 'skipped',
      items: 'skipped',
      signer: 'skipped',
      signature: 'skipped',
      problems: pending ? [] : ['Действие есть в БД, но нет в цепи'],
    },
    blockNumber: null,
    dbIndex,
  }
}

async function appliedStage(
  id: string,
  dbAction: DbAction,
  chainAction: AppliedAction,
  chain: ChainReader,
  dbIndex: number,
): Promise<Entry> {
  const problems: string[] = []
  const stage: AuditStage = {
    actionId: id,
    actionType: chainAction.actionType,
    status: 'applied',
    operator: dbAction.operator,
    operatorId: dbAction.operatorId,
    orgId: chainAction.orgId,
    orgName: (await chain.organizationName(chainAction.orgId)) ?? dbAction.orgName,
    blockNumber: chainAction.blockNumber,
    blockTime: await chain.blockTime(chainAction.blockNumber),
    txHash: chainAction.txHash,
    rejectReason: dbAction.rejectReason,
    detailsHash: 'ok',
    items: 'ok',
    signer: 'ok',
    signature: 'ok',
    problems,
  }

  if (dbAction.details === null) {
    stage.detailsHash = 'mismatch'
    problems.push(
      (dbAction.detailsError && DETAILS_ERROR_TEXT[dbAction.detailsError]) ??
        'Хеш деталей из БД недоступен',
    )
  } else if (chainAction.actionType !== dbAction.actionType) {
    stage.detailsHash = 'mismatch'
    problems.push('Тип действия в БД не совпадает с цепью')
  } else if (
    detailsHash(chainAction.actionType, { ...dbAction.details, salt: dbAction.salt }).toLowerCase() !==
    chainAction.detailsHash.toLowerCase()
  ) {
    stage.detailsHash = 'mismatch'
    problems.push('Хеш деталей из БД не совпадает с цепью')
  }

  if (
    chainAction.keyId.toLowerCase() === dbAction.keyId.toLowerCase() &&
    chainAction.orgId.toLowerCase() === dbAction.orgId.toLowerCase()
  ) {
    stage.signer = 'ok'
  } else {
    stage.signer = 'mismatch'
    problems.push('Подписант в БД не совпадает с цепью')
  }

  const auth = await chain.actionAuth(chainAction.txHash, id)
  if (auth === null) {
    stage.signature = 'mismatch'
    stage.items = 'mismatch'
    problems.push('Транзакция с действием не найдена в цепи')
  } else {
    const key = await chain.key(chainAction.keyId)
    const expectedChallenge = challenge(id, chainAction.actionType, itemsHash(auth.itemIds), chainAction.detailsHash)
    stage.signature = (await verifyWebAuthn(auth.webauthn, expectedChallenge, key.qx, key.qy)) ? 'ok' : 'mismatch'
    if (stage.signature === 'mismatch') problems.push('Подпись не проходит проверку')
    stage.items = sameItems(auth.itemIds, dbAction.itemIds) ? 'ok' : 'mismatch'
    if (stage.items === 'mismatch') problems.push('Список товаров в БД не совпадает с цепью')
  }

  return { stage, blockNumber: chainAction.blockNumber, dbIndex }
}

function sameItems(chainItems: readonly bigint[], dbItemIds: readonly string[]): boolean {
  if (chainItems.length !== dbItemIds.length) return false
  if (new Set(dbItemIds).size !== dbItemIds.length) return false
  const onChain = new Set(chainItems.map(String))
  return dbItemIds.every((uuid) => onChain.has(itemId(uuid).toString()))
}
