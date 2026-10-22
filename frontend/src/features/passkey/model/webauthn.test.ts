import { describe, expect, it } from 'vitest'

import {
  base64urlToBuffer,
  bufferToBase64url,
  credentialToJSON,
  toCreationOptions,
  toRequestOptions,
  webauthnErrorText,
  type CreationOptionsJSON,
} from './webauthn'
import type { KeyActionDraft } from './types'

describe('base64url', () => {
  it('round-trips byte arrays of length 0..5 without padding', () => {
    for (let len = 0; len <= 5; len++) {
      const bytes = Uint8Array.from({ length: len }, (_, i) => (i * 37 + 250) & 0xff)
      const encoded = bufferToBase64url(bytes)
      expect(encoded).not.toMatch(/[+/=]/)
      expect(new Uint8Array(base64urlToBuffer(encoded))).toEqual(bytes)
    }
  })

  it('uses url-safe alphabet for bytes 0xfb 0xff', () => {
    const encoded = bufferToBase64url(Uint8Array.from([0xfb, 0xff]))
    expect(encoded).toBe('-_8')
    expect(new Uint8Array(base64urlToBuffer(encoded))).toEqual(Uint8Array.from([0xfb, 0xff]))
  })
})

describe('toCreationOptions', () => {
  it('decodes challenge, user.id and excludeCredentials ids', () => {
    const json: CreationOptionsJSON = {
      challenge: '-_8',
      rp: { name: 'WMS' },
      user: { id: 'AQID', name: 'alice', displayName: 'Alice' },
      pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
      excludeCredentials: [{ type: 'public-key', id: 'BAU' }],
      authenticatorSelection: { userVerification: 'required' },
      attestation: 'none',
      timeout: 60000,
    }
    const options = toCreationOptions(json)
    expect(new Uint8Array(options.challenge as ArrayBuffer)).toEqual(Uint8Array.from([0xfb, 0xff]))
    expect(new Uint8Array(options.user.id as ArrayBuffer)).toEqual(Uint8Array.from([1, 2, 3]))
    expect(new Uint8Array(options.excludeCredentials?.[0].id as ArrayBuffer)).toEqual(
      Uint8Array.from([4, 5])
    )
    expect(options.pubKeyCredParams).toEqual(json.pubKeyCredParams)
    expect(options.authenticatorSelection).toEqual({ userVerification: 'required' })
    expect(options.rp).toEqual({ name: 'WMS' })
    expect(options.user.name).toBe('alice')
  })
})

describe('toRequestOptions', () => {
  it('decodes challenge and credential ids, copies rpId, userVerification and timeout', () => {
    const draft: KeyActionDraft = {
      actionId: '01929c5e-6f00-7000-8000-000000001001',
      actionType: 5,
      challenge: '-_8',
      rpId: 'localhost',
      allowCredentials: [{ type: 'public-key', id: 'AQID' }],
      userVerification: 'required',
      timeout: 60000,
    }
    const options = toRequestOptions(draft)
    expect(new Uint8Array(options.challenge as ArrayBuffer)).toEqual(Uint8Array.from([0xfb, 0xff]))
    expect(options.rpId).toBe('localhost')
    expect(options.allowCredentials).toHaveLength(1)
    expect(new Uint8Array(options.allowCredentials?.[0].id as ArrayBuffer)).toEqual(
      Uint8Array.from([1, 2, 3])
    )
    expect(options.userVerification).toBe('required')
    expect(options.timeout).toBe(60000)
  })
})

describe('credentialToJSON', () => {
  it('serializes a fake credential to base64url fields', () => {
    const fake = {
      id: 'AQID',
      rawId: Uint8Array.from([1, 2, 3]).buffer,
      type: 'public-key',
      authenticatorAttachment: 'platform',
      response: {
        clientDataJSON: Uint8Array.from([0xfb, 0xff]).buffer,
        attestationObject: Uint8Array.from([4, 5]).buffer,
        getTransports: () => ['internal'],
      },
      getClientExtensionResults: () => ({}),
    } as unknown as PublicKeyCredential

    expect(credentialToJSON(fake)).toEqual({
      id: 'AQID',
      rawId: 'AQID',
      type: 'public-key',
      response: {
        clientDataJSON: '-_8',
        attestationObject: 'BAU',
        transports: ['internal'],
      },
      clientExtensionResults: {},
      authenticatorAttachment: 'platform',
    })
  })

  it('falls back to empty transports and omits null attachment', () => {
    const fake = {
      id: 'AQID',
      rawId: Uint8Array.from([1, 2, 3]).buffer,
      type: 'public-key',
      authenticatorAttachment: null,
      response: {
        clientDataJSON: new ArrayBuffer(0),
        attestationObject: new ArrayBuffer(0),
      },
      getClientExtensionResults: () => ({}),
    } as unknown as PublicKeyCredential

    const json = credentialToJSON(fake)
    expect(json.response.transports).toEqual([])
    expect(json).not.toHaveProperty('authenticatorAttachment')
  })
})

describe('webauthnErrorText', () => {
  it('maps NotAllowedError to a user message', () => {
    expect(webauthnErrorText(new DOMException('x', 'NotAllowedError'))).toBe(
      'Создание ключа отменено или истекло время ожидания'
    )
  })

  it('returns null for a plain Error', () => {
    expect(webauthnErrorText(new Error('boom'))).toBeNull()
  })
})
