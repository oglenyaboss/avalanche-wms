// WebAuthn tuple verification checked against the golden vectors.
import { describe, expect, it } from 'vitest'
import { bytesToHex, hexToBigInt, hexToBytes, toHex, type Hex } from 'viem'

// The golden vectors live in the repo root, outside the @/ alias.
// eslint-disable-next-line no-restricted-imports
import vectorsRaw from '../../../../../testdata/signed-actions/vectors.v1.json?raw'
import { verifyWebAuthn, type WebAuthnTuple } from './webauthnVerify'

const SECP256R1_N = 0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551n

type ActionVector = {
  actionId: string
  actionType: number
  challenge: Hex
  name: string
  webauthn: {
    authenticatorData: Hex
    clientDataJSONHex: Hex
    challengeIndex: number
    typeIndex: number
    r: Hex
    s: Hex
  }
}

type Vectors = {
  actions: ActionVector[]
  signer: { qx: Hex; qy: Hex }
  subjectKey: { qx: Hex; qy: Hex }
}

const vectors = JSON.parse(vectorsRaw) as Vectors

function tupleOf(action: ActionVector): WebAuthnTuple {
  const wa = action.webauthn
  return {
    authenticatorData: wa.authenticatorData,
    clientDataJSON: new TextDecoder().decode(hexToBytes(wa.clientDataJSONHex)),
    challengeIndex: wa.challengeIndex,
    typeIndex: wa.typeIndex,
    r: wa.r,
    s: wa.s,
  }
}

const receive = vectors.actions.find((action) => action.name === 'receive')!

describe('verifyWebAuthn', () => {
  it('verifies every assertion with the signer key and vector challenge', async () => {
    for (const action of vectors.actions) {
      const ok = await verifyWebAuthn(tupleOf(action), action.challenge, vectors.signer.qx, vectors.signer.qy)
      expect(ok, action.name).toBe(true)
    }
  })

  it('rejects a flipped challenge byte', async () => {
    const challenge = hexToBytes(receive.challenge)
    challenge[31] ^= 1
    expect(await verifyWebAuthn(tupleOf(receive), bytesToHex(challenge), vectors.signer.qx, vectors.signer.qy)).toBe(
      false,
    )
  })

  it('rejects a high-s signature', async () => {
    const tuple = tupleOf(receive)
    const highS = toHex(SECP256R1_N - hexToBigInt(tuple.s), { size: 32 })
    expect(await verifyWebAuthn({ ...tuple, s: highS }, receive.challenge, vectors.signer.qx, vectors.signer.qy)).toBe(
      false,
    )
  })

  it('rejects a cleared UV flag', async () => {
    const tuple = tupleOf(receive)
    const authenticatorData = hexToBytes(tuple.authenticatorData)
    authenticatorData[32] &= ~0x04
    expect(
      await verifyWebAuthn(
        { ...tuple, authenticatorData: bytesToHex(authenticatorData) },
        receive.challenge,
        vectors.signer.qx,
        vectors.signer.qy,
      ),
    ).toBe(false)
  })

  it('rejects a flipped r', async () => {
    const tuple = tupleOf(receive)
    const r = hexToBytes(tuple.r)
    r[0] ^= 1
    expect(await verifyWebAuthn({ ...tuple, r: bytesToHex(r) }, receive.challenge, vectors.signer.qx, vectors.signer.qy)).toBe(
      false,
    )
  })

  it('rejects a wrong public key', async () => {
    expect(await verifyWebAuthn(tupleOf(receive), receive.challenge, vectors.subjectKey.qx, vectors.signer.qy)).toBe(
      false,
    )
  })
})
