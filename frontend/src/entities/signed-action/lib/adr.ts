// Hash primitives of ADR-0001 signed actions in the browser, mirroring
// wms/internal/signedaction/challenge.go.
import {
  concatHex,
  encodeAbiParameters,
  hexToBigInt,
  keccak256,
  toBytes,
  toHex,
  type Hex,
} from 'viem'

export const ACTION_TYPE = {
  Receive: 1,
  PutAway: 2,
  Pick: 3,
  Ship: 4,
  KeyRegister: 5,
  KeyRevoke: 6,
} as const

export type ActionDetails = {
  salt: Hex
  cargoplaceId?: string
  storageBinId?: string
  shippingBufferBinId?: string
  dispatchId?: string
  orderIds?: string[]
  operatorId: string
  occurredAtMs: number
}

export function uuidToBytes16(uuid: string): Hex {
  return `0x${uuid.replaceAll('-', '')}`
}

export function itemId(productId: string): bigint {
  return hexToBigInt(keccak256(toBytes(productId.toLowerCase())))
}

export function itemsHash(itemIds: readonly bigint[]): Hex {
  return keccak256(concatHex(itemIds.map((id) => toHex(id, { size: 32 }))))
}

export function detailsHash(actionType: number, d: ActionDetails): Hex {
  const operator = uuidToBytes16(d.operatorId)
  switch (actionType) {
    case ACTION_TYPE.Receive:
      return keccak256(
        encodeAbiParameters(
          [{ type: 'bytes32' }, { type: 'bytes16' }, { type: 'bytes16' }, { type: 'uint64' }],
          [d.salt, uuidToBytes16(d.cargoplaceId!), operator, BigInt(d.occurredAtMs)],
        ),
      )
    case ACTION_TYPE.PutAway:
      return keccak256(
        encodeAbiParameters(
          [{ type: 'bytes32' }, { type: 'bytes16' }, { type: 'bytes16' }, { type: 'uint64' }],
          [d.salt, uuidToBytes16(d.storageBinId!), operator, BigInt(d.occurredAtMs)],
        ),
      )
    case ACTION_TYPE.Pick:
      return keccak256(
        encodeAbiParameters(
          [
            { type: 'bytes32' },
            { type: 'bytes16' },
            { type: 'bytes16[]' },
            { type: 'bytes16' },
            { type: 'uint64' },
          ],
          [d.salt, uuidToBytes16(d.shippingBufferBinId!), sortedOrderIds(d), operator, BigInt(d.occurredAtMs)],
        ),
      )
    case ACTION_TYPE.Ship:
      return keccak256(
        encodeAbiParameters(
          [
            { type: 'bytes32' },
            { type: 'bytes16' },
            { type: 'bytes16' },
            { type: 'bytes16[]' },
            { type: 'bytes16' },
            { type: 'uint64' },
          ],
          [
            d.salt,
            uuidToBytes16(d.dispatchId!),
            uuidToBytes16(d.shippingBufferBinId!),
            sortedOrderIds(d),
            operator,
            BigInt(d.occurredAtMs),
          ],
        ),
      )
    default:
      throw new Error(`detailsHash does not cover action type ${actionType}`)
  }
}

// Ascending by raw bytes without duplicates, as Go's sortedUniqueOrderIDs.
function sortedOrderIds(d: ActionDetails): Hex[] {
  const ids = [...new Set((d.orderIds ?? []).map((id) => id.toLowerCase()))]
  ids.sort()
  return ids.map(uuidToBytes16)
}

const TYPEHASH = keccak256(toBytes('avalanche-wms/SignedAction/v1'))

export function challenge(
  actionId: string,
  actionType: number,
  itemsHashHex: Hex,
  detailsHashHex: Hex,
): Hex {
  return keccak256(
    encodeAbiParameters(
      [
        { type: 'bytes32' },
        { type: 'bytes16' },
        { type: 'uint8' },
        { type: 'bytes32' },
        { type: 'bytes32' },
      ],
      [TYPEHASH, uuidToBytes16(actionId), actionType, itemsHashHex, detailsHashHex],
    ),
  )
}
