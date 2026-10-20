// Hash primitives checked against the golden vectors of the Go implementation.
import { describe, expect, it } from 'vitest'
import { hexToBigInt, type Hex } from 'viem'

// The golden vectors live in the repo root, outside the @/ alias.
// eslint-disable-next-line no-restricted-imports
import vectorsRaw from '../../../../../testdata/signed-actions/vectors.v1.json?raw'
import { challenge, detailsHash, itemId, itemsHash, type ActionDetails } from './adr'

type ActionVector = {
  actionId: string
  actionType: number
  itemIds: Hex[]
  itemsHash: Hex
  detailsHash: Hex
  challenge: Hex
  name: string
  details: Partial<ActionDetails> & { qx?: Hex; qy?: Hex }
}

type Vectors = {
  actions: ActionVector[]
  products: { productId: string; itemId: Hex }[]
}

const vectors = JSON.parse(vectorsRaw) as Vectors

describe('itemId', () => {
  it('matches products[].itemId in the vectors', () => {
    for (const product of vectors.products) {
      expect(itemId(product.productId)).toBe(hexToBigInt(product.itemId))
    }
  })
})

describe('itemsHash', () => {
  it('matches the vectors for every action', () => {
    for (const action of vectors.actions) {
      expect(itemsHash(action.itemIds.map((id) => hexToBigInt(id)))).toBe(action.itemsHash)
    }
  })
})

describe('detailsHash', () => {
  it('matches the vectors for every warehouse action', () => {
    for (const action of vectors.actions) {
      if (action.actionType > 4) continue
      expect(detailsHash(action.actionType, action.details as ActionDetails)).toBe(action.detailsHash)
    }
  })

  it('throws for key actions', () => {
    const details: ActionDetails = { salt: '0x00', operatorId: '0', occurredAtMs: 0 }
    expect(() => detailsHash(5, details)).toThrow()
  })
})

describe('challenge', () => {
  it('matches the vectors for every action', () => {
    for (const action of vectors.actions) {
      expect(challenge(action.actionId, action.actionType, action.itemsHash, action.detailsHash)).toBe(
        action.challenge,
      )
    }
  })
})
