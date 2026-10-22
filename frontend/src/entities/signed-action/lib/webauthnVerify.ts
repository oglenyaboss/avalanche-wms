// Contract-side WebAuthn assertion verification (ADR §5), mirroring
// VerifyOnChain in wms/internal/signedaction/onchain.go.
import { hexToBigInt, hexToBytes, type Hex } from 'viem'

import { bufferToBase64url } from '@/shared/lib'

export type WebAuthnTuple = {
  authenticatorData: Hex
  clientDataJSON: string
  challengeIndex: number
  typeIndex: number
  r: Hex
  s: Hex
}

const SECP256R1_N = 0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551n
const TYPE_PREFIX = '"type":"webauthn.get"'

export async function verifyWebAuthn(
  auth: WebAuthnTuple,
  challengeHex: Hex,
  qx: Hex,
  qy: Hex,
): Promise<boolean> {
  try {
    if (!hasPrefixAt(auth.clientDataJSON, TYPE_PREFIX, auth.typeIndex)) return false
    const challengePrefix = `"challenge":"${bufferToBase64url(hexToBytes(challengeHex))}"`
    if (!hasPrefixAt(auth.clientDataJSON, challengePrefix, auth.challengeIndex)) return false

    const authenticatorData = hexToBytes(auth.authenticatorData)
    if (authenticatorData.length < 37) return false
    const flags = authenticatorData[32]
    if ((flags & 0x01) === 0 || (flags & 0x04) === 0) return false
    if (hexToBigInt(auth.s) > SECP256R1_N >> 1n) return false

    const subtle = globalThis.crypto.subtle
    const publicKey = new Uint8Array(65)
    publicKey[0] = 0x04
    publicKey.set(hexToBytes(qx), 1)
    publicKey.set(hexToBytes(qy), 33)
    const key = await subtle.importKey(
      'raw',
      publicKey,
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['verify'],
    )

    const signature = new Uint8Array(64)
    signature.set(hexToBytes(auth.r), 0)
    signature.set(hexToBytes(auth.s), 32)

    // WebCrypto hashes the data itself, so pass the preimage
    // authenticatorData ‖ sha256(clientDataJSON).
    const clientDataHash = new Uint8Array(
      await subtle.digest('SHA-256', new TextEncoder().encode(auth.clientDataJSON)),
    )
    const signed = new Uint8Array(authenticatorData.length + clientDataHash.length)
    signed.set(authenticatorData, 0)
    signed.set(clientDataHash, authenticatorData.length)

    return await subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, key, signature, signed)
  } catch {
    return false
  }
}

function hasPrefixAt(text: string, prefix: string, index: number): boolean {
  return index >= 0 && index <= text.length && text.startsWith(prefix, index)
}
