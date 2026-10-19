// @vitest-environment jsdom
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest'

import { signOperation } from './signOperation'
import type { SignedOperationDraft } from './types'

// getAssertion checks `cred instanceof PublicKeyCredential`, which jsdom does
// not define — stub both the constructor and navigator.credentials.get.
class FakePublicKeyCredential {}

function fakeCredential(startByte: number) {
  const credential = new FakePublicKeyCredential() as unknown as PublicKeyCredential
  Object.defineProperty(credential, 'rawId', {
    value: Uint8Array.from([startByte]).buffer,
  })
  Object.defineProperty(credential, 'response', {
    value: {
      authenticatorData: Uint8Array.from([startByte + 1]).buffer,
      clientDataJSON: Uint8Array.from([startByte + 2]).buffer,
      signature: Uint8Array.from([startByte + 3]).buffer,
    },
  })
  return credential
}

const credentialsGet = vi.fn()

beforeAll(() => {
  vi.stubGlobal('PublicKeyCredential', FakePublicKeyCredential)
  Object.defineProperty(window.navigator, 'credentials', {
    configurable: true,
    value: { get: credentialsGet },
  })
})

afterAll(() => {
  vi.unstubAllGlobals()
})

afterEach(() => {
  credentialsGet.mockReset()
})

const draft: SignedOperationDraft = {
  operationId: '01929c5e-op-1',
  actionType: 3,
  signerKeyId: '0xabc',
  rpId: 'localhost',
  allowCredentials: [{ type: 'public-key', id: 'AQID' }],
  userVerification: 'required',
  timeout: 60000,
  parts: [
    { actionId: '01929c5e-a-1', itemIds: ['01929c5e-i-1'], detailsHash: '0x1', challenge: 'AQID' },
    { actionId: '01929c5e-a-2', itemIds: [], detailsHash: '0x2', challenge: 'Aw' },
  ],
}

describe('signOperation', () => {
  it('signs each part with its own authenticator touch, in draft order', async () => {
    credentialsGet
      .mockResolvedValueOnce(fakeCredential(1))
      .mockResolvedValueOnce(fakeCredential(5))

    const assertions = await signOperation(draft)

    expect(credentialsGet).toHaveBeenCalledTimes(2)

    const [first, second] = credentialsGet.mock.calls.map((call) => call[0].publicKey)
    // Part challenges reach the authenticator in draft order.
    expect(new Uint8Array(first.challenge as ArrayBuffer)).toEqual(Uint8Array.from([1, 2, 3]))
    expect(new Uint8Array(second.challenge as ArrayBuffer)).toEqual(Uint8Array.from([3]))
    expect(first.rpId).toBe('localhost')
    expect(first.userVerification).toBe('required')
    expect(first.timeout).toBe(60000)
    expect(new Uint8Array(first.allowCredentials![0].id as ArrayBuffer)).toEqual(
      Uint8Array.from([1, 2, 3])
    )

    // Assertions come back aligned with the parts they were signed for.
    expect(assertions).toEqual([
      {
        actionId: '01929c5e-a-1',
        credentialId: 'AQ',
        authenticatorData: 'Ag',
        clientDataJson: 'Aw',
        signature: 'BA',
      },
      {
        actionId: '01929c5e-a-2',
        credentialId: 'BQ',
        authenticatorData: 'Bg',
        clientDataJson: 'Bw',
        signature: 'CA',
      },
    ])
  })
})
