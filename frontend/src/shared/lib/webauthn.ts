// WebAuthn assertion (signing) primitives shared across features: the passkey
// key-confirmation flow and the receiving close flow both build getRequest
// options from a base64url draft and serialize the resulting assertion.

export interface AssertionJSON {
  credentialId: string
  authenticatorData: string
  clientDataJson: string
  signature: string
}

export function base64urlToBuffer(s: string): ArrayBuffer {
  const base64 = s.replace(/-/g, '+').replace(/_/g, '/')
  const binary = atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, '='))
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

export function bufferToBase64url(b: ArrayBuffer | Uint8Array): string {
  const bytes = b instanceof Uint8Array ? b : new Uint8Array(b)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

// Structural input for building assertion request options: the passkey
// KeyActionDraft and a signed-operation part draft both fit this shape.
export interface AssertionRequestInput {
  challenge: string
  rpId: string
  allowCredentials: { type: 'public-key'; id: string }[]
  userVerification: UserVerificationRequirement
  timeout: number
}

export function toRequestOptions(input: AssertionRequestInput): PublicKeyCredentialRequestOptions {
  return {
    challenge: base64urlToBuffer(input.challenge),
    rpId: input.rpId,
    allowCredentials: input.allowCredentials.map((c) => ({
      type: c.type,
      id: base64urlToBuffer(c.id),
    })),
    userVerification: input.userVerification,
    timeout: input.timeout,
  }
}

export async function getAssertion(
  options: PublicKeyCredentialRequestOptions
): Promise<AssertionJSON> {
  const cred = await navigator.credentials.get({ publicKey: options })
  if (!(cred instanceof PublicKeyCredential)) {
    throw new Error('Браузер не вернул подпись')
  }
  const response = cred.response as AuthenticatorAssertionResponse
  return {
    credentialId: bufferToBase64url(cred.rawId),
    authenticatorData: bufferToBase64url(response.authenticatorData),
    clientDataJson: bufferToBase64url(response.clientDataJSON),
    signature: bufferToBase64url(response.signature),
  }
}

// Signing-step DOMException → operator-facing text. NotAllowedError covers both
// a dismissed authenticator prompt and a timeout; any other WebAuthn failure
// gets the generic signing text. Non-DOMException errors (API, network) return
// null so callers fall back to their own error classification.
export function webauthnSignErrorText(e: unknown): string | null {
  if (!(e instanceof DOMException)) return null
  if (e.name === 'NotAllowedError') {
    return 'Подтверждение отменено или истекло время ожидания'
  }
  return 'Не удалось получить подпись'
}
