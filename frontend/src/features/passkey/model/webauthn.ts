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

export interface CreationOptionsJSON
  extends Omit<PublicKeyCredentialCreationOptions, 'challenge' | 'user' | 'excludeCredentials'> {
  challenge: string
  user: Omit<PublicKeyCredentialUserEntity, 'id'> & { id: string }
  excludeCredentials?: (Omit<PublicKeyCredentialDescriptor, 'id'> & { id: string })[]
}

export interface RegistrationResponseJSON {
  id: string
  rawId: string
  type: string
  response: {
    clientDataJSON: string
    attestationObject: string
    transports: string[]
  }
  clientExtensionResults: AuthenticationExtensionsClientOutputs
  authenticatorAttachment?: string
}

export function toCreationOptions(json: CreationOptionsJSON): PublicKeyCredentialCreationOptions {
  return {
    ...json,
    challenge: base64urlToBuffer(json.challenge),
    user: { ...json.user, id: base64urlToBuffer(json.user.id) },
    excludeCredentials: json.excludeCredentials?.map((c) => ({
      ...c,
      id: base64urlToBuffer(c.id),
    })),
  }
}

export function credentialToJSON(cred: PublicKeyCredential): RegistrationResponseJSON {
  const response = cred.response as AuthenticatorAttestationResponse
  const json: RegistrationResponseJSON = {
    id: cred.id,
    rawId: bufferToBase64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      attestationObject: bufferToBase64url(response.attestationObject),
      transports: typeof response.getTransports === 'function' ? response.getTransports() : [],
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  }
  if (cred.authenticatorAttachment) json.authenticatorAttachment = cred.authenticatorAttachment
  return json
}

export async function createPasskeyCredential(
  options: PublicKeyCredentialCreationOptions
): Promise<RegistrationResponseJSON> {
  const cred = await navigator.credentials.create({ publicKey: options })
  if (!(cred instanceof PublicKeyCredential)) {
    throw new Error('Браузер не вернул ключ')
  }
  return credentialToJSON(cred)
}

export function webauthnErrorText(e: unknown): string | null {
  if (!(e instanceof DOMException)) return null
  switch (e.name) {
    case 'NotAllowedError':
      return 'Создание ключа отменено или истекло время ожидания'
    case 'InvalidStateError':
      return 'Этот ключ уже зарегистрирован на устройстве'
    case 'SecurityError':
      return 'Браузер запретил создание ключа на этом адресе'
    default:
      return null
  }
}
