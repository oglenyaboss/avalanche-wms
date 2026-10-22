// WebAuthn browser-API glue for passkey registration. The assertion
// (signing) half — base64url codecs, getRequest options, getAssertion — lives
// in @/shared/lib because the signed-action flow signs with the same
// primitives; those pieces are re-exported to keep this module's consumers
// (usePasskey, tests) stable.
import { base64urlToBuffer, bufferToBase64url } from '@/shared/lib'

export {
  base64urlToBuffer,
  bufferToBase64url,
  getAssertion,
  toRequestOptions,
} from '@/shared/lib'

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

export function assertionErrorText(e: unknown): string | null {
  if (!(e instanceof DOMException)) return null
  switch (e.name) {
    case 'NotAllowedError':
      return 'Подтверждение отменено или истекло время ожидания'
    case 'SecurityError':
      return 'Браузер запретил подпись на этом адресе'
    default:
      return null
  }
}
