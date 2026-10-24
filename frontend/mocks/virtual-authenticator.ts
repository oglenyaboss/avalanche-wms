import { bytesToHex, type Hex } from "viem";

import { fromBase64url, toBase64url } from "@/lib/signing/base64url";
import { rawToDer } from "@/lib/signing/webauthn-verify";

import { seededKeys } from "./data/keys";

export type AuthenticatorMode = "ok" | "cancel" | "corrupt";

type StoredCredential = { credentialId: string; jwk: JsonWebKey };

const storageKey = "wms-virtual-authenticator";
const modeKey = "wms-virtual-authenticator-mode";

function stored(): StoredCredential[] {
  return JSON.parse(localStorage.getItem(storageKey) ?? "[]");
}

function allCredentials(): StoredCredential[] {
  return [...seededKeys, ...stored()];
}

export function authenticatorMode(): AuthenticatorMode {
  return (localStorage.getItem(modeKey) as AuthenticatorMode | null) ?? "ok";
}

export function setAuthenticatorMode(mode: AuthenticatorMode) {
  localStorage.setItem(modeKey, mode);
}

export function virtualPublicKey(credentialId: string): { qx: Hex; qy: Hex } | null {
  const credential = allCredentials().find((c) => c.credentialId === credentialId);
  if (!credential?.jwk.x || !credential.jwk.y) return null;
  return { qx: bytesToHex(fromBase64url(credential.jwk.x)), qy: bytesToHex(fromBase64url(credential.jwk.y)) };
}

const notAllowed = () => new DOMException("Пользователь отменил подтверждение", "NotAllowedError");

async function authenticatorData(rpId: string, flags: number): Promise<Uint8Array<ArrayBuffer>> {
  const rpIdHash = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(rpId)));
  return new Uint8Array([...rpIdHash, flags, 0, 0, 0, 1]);
}

function clientData(type: "webauthn.get" | "webauthn.create", challenge: BufferSource): Uint8Array<ArrayBuffer> {
  const bytes = challenge instanceof ArrayBuffer ? new Uint8Array(challenge) : new Uint8Array(challenge.buffer, challenge.byteOffset, challenge.byteLength);
  const json = `{"type":"${type}","challenge":"${toBase64url(bytes)}","origin":"${location.origin}","crossOrigin":false}`;
  return new TextEncoder().encode(json);
}

async function get(options?: CredentialRequestOptions): Promise<Credential | null> {
  const request = options?.publicKey;
  if (!request || authenticatorMode() === "cancel") throw notAllowed();
  const allowed = (request.allowCredentials ?? []).map((c) => toBase64url(c.id as ArrayBuffer | Uint8Array));
  const credential = allCredentials().find((c) => allowed.includes(c.credentialId));
  if (!credential) throw notAllowed();

  const key = await crypto.subtle.importKey("jwk", credential.jwk, { name: "ECDSA", namedCurve: "P-256" }, false, ["sign"]);
  const authData = await authenticatorData(request.rpId ?? location.hostname, 0x05);
  const clientDataJSON = clientData("webauthn.get", request.challenge);
  const clientDataHash = new Uint8Array(await crypto.subtle.digest("SHA-256", clientDataJSON));
  const raw = new Uint8Array(await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, key, new Uint8Array([...authData, ...clientDataHash])));
  if (authenticatorMode() === "corrupt") raw[0] ^= 0xff;

  return publicKeyCredential(credential.credentialId, {
    authenticatorData: authData.buffer,
    clientDataJSON: clientDataJSON.buffer,
    signature: rawToDer(raw).buffer,
    userHandle: null,
  });
}

async function create(options?: CredentialCreationOptions): Promise<Credential | null> {
  const request = options?.publicKey;
  if (!request || authenticatorMode() === "cancel") throw notAllowed();
  const excluded = (request.excludeCredentials ?? []).map((c) => toBase64url(c.id as ArrayBuffer | Uint8Array));
  if (allCredentials().some((c) => excluded.includes(c.credentialId))) {
    throw new DOMException("Ключ уже зарегистрирован на устройстве", "InvalidStateError");
  }

  const pair = await crypto.subtle.generateKey({ name: "ECDSA", namedCurve: "P-256" }, true, ["sign", "verify"]);
  const jwk = await crypto.subtle.exportKey("jwk", pair.privateKey);
  const credentialId = toBase64url(crypto.getRandomValues(new Uint8Array(16)));
  localStorage.setItem(storageKey, JSON.stringify([...stored(), { credentialId, jwk: { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y, d: jwk.d } }]));

  // ponytail: attestationObject не CBOR — мок читает публичный ключ из хранилища аутентификатора (virtualPublicKey); настоящий бэкенд такой ответ отклонит
  const attestationObject = new TextEncoder().encode(`virtual:${credentialId}`);
  return publicKeyCredential(credentialId, {
    clientDataJSON: clientData("webauthn.create", request.challenge).buffer,
    attestationObject: attestationObject.buffer,
    getTransports: () => ["internal"],
  });
}

function publicKeyCredential(credentialId: string, response: object): Credential {
  return {
    id: credentialId,
    rawId: fromBase64url(credentialId).buffer,
    type: "public-key",
    authenticatorAttachment: "platform",
    response,
    getClientExtensionResults: () => ({}),
  } as unknown as Credential;
}

export function installVirtualAuthenticator() {
  Object.defineProperty(navigator, "credentials", {
    configurable: true,
    value: { get, create, store: async () => undefined, preventSilentAccess: async () => undefined },
  });
}

export function resetVirtualAuthenticator() {
  localStorage.removeItem(storageKey);
  localStorage.removeItem(modeKey);
}

export function virtualCredentialIdFor(username: string): string | undefined {
  return seededKeys.find((key) => key.username === username)?.credentialId;
}

