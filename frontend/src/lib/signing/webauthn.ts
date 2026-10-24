import type { Schema } from "@/lib/api/client";

import { fromBase64url, toBase64url } from "./base64url";

export class SigningError extends Error {
  override readonly name = "SigningError";

  constructor(
    readonly code: "SIGNING_CANCELLED" | "SIGNING_FAILED",
    message: string,
  ) {
    super(message);
  }
}

export type AssertionRequest = {
  challenge: string;
  rp_id: string;
  allow_credentials: { type: "public-key"; id: string }[];
  user_verification: "required";
  timeout: number;
};

export async function getAssertion(request: AssertionRequest): Promise<Schema<"WebAuthnAssertion">> {
  const credential = await callAuthenticator(() =>
    navigator.credentials.get({
      publicKey: {
        challenge: fromBase64url(request.challenge),
        rpId: request.rp_id,
        allowCredentials: request.allow_credentials.map((c) => ({ type: c.type, id: fromBase64url(c.id) })),
        userVerification: request.user_verification,
        timeout: request.timeout,
      },
    }),
  );
  const response = credential.response as AuthenticatorAssertionResponse;
  return {
    credential_id: toBase64url(credential.rawId),
    authenticator_data: toBase64url(response.authenticatorData),
    client_data_json: toBase64url(response.clientDataJSON),
    signature: toBase64url(response.signature),
  };
}

export async function createPasskey(
  options: Schema<"PasskeyCreationOptions">,
): Promise<Schema<"PasskeyRegistrationResponse">> {
  const { publicKey } = options;
  const credential = await callAuthenticator(() =>
    navigator.credentials.create({
      publicKey: {
        ...publicKey,
        challenge: fromBase64url(publicKey.challenge),
        user: { ...publicKey.user, id: fromBase64url(publicKey.user.id) },
        excludeCredentials: publicKey.excludeCredentials?.map((c) => ({ type: c.type, id: fromBase64url(c.id) })),
      },
    }),
  );
  const response = credential.response as AuthenticatorAttestationResponse;
  return {
    id: credential.id,
    rawId: toBase64url(credential.rawId),
    type: "public-key",
    response: {
      clientDataJSON: toBase64url(response.clientDataJSON),
      attestationObject: toBase64url(response.attestationObject),
      transports: response.getTransports?.() ?? [],
    },
    clientExtensionResults: credential.getClientExtensionResults() as Record<string, unknown>,
    authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
  };
}

async function callAuthenticator(call: () => Promise<Credential | null>): Promise<PublicKeyCredential> {
  try {
    const credential = await call();
    if (!credential) throw new SigningError("SIGNING_FAILED", "Устройство не вернуло ключ");
    return credential as PublicKeyCredential;
  } catch (error) {
    if (error instanceof SigningError) throw error;
    if (error instanceof DOMException && error.name === "NotAllowedError") {
      throw new SigningError("SIGNING_CANCELLED", "Подтверждение отменено или истекло время ожидания");
    }
    throw new SigningError("SIGNING_FAILED", "Не удалось получить подпись");
  }
}
