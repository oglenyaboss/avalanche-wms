import { bytesToHex, hexToBigInt, hexToBytes, toHex, type Hex } from "viem";

import { fromBase64url, toBase64url } from "./base64url";

export type WebAuthnTuple = {
  authenticatorData: Hex;
  clientDataJSON: string;
  challengeIndex: number;
  typeIndex: number;
  r: Hex;
  s: Hex;
};

const secp256r1N = 0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551n;
const typePrefix = '"type":"webauthn.get"';

export async function verifyWebAuthn(auth: WebAuthnTuple, challengeHex: Hex, qx: Hex, qy: Hex): Promise<boolean> {
  try {
    if (!hasPrefixAt(auth.clientDataJSON, typePrefix, auth.typeIndex)) return false;
    const challengePrefix = `"challenge":"${toBase64url(hexToBytes(challengeHex))}"`;
    if (!hasPrefixAt(auth.clientDataJSON, challengePrefix, auth.challengeIndex)) return false;

    const authenticatorData = hexToBytes(auth.authenticatorData);
    if (authenticatorData.length < 37) return false;
    const flags = authenticatorData[32];
    if ((flags & 0x01) === 0 || (flags & 0x04) === 0) return false;
    if (hexToBigInt(auth.s) > secp256r1N >> 1n) return false;

    const publicKey = new Uint8Array(65);
    publicKey[0] = 0x04;
    publicKey.set(hexToBytes(qx), 1);
    publicKey.set(hexToBytes(qy), 33);
    const key = await crypto.subtle.importKey("raw", publicKey, { name: "ECDSA", namedCurve: "P-256" }, false, ["verify"]);

    const signature = new Uint8Array(64);
    signature.set(hexToBytes(auth.r), 0);
    signature.set(hexToBytes(auth.s), 32);

    const clientDataHash = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(auth.clientDataJSON)));
    const signed = new Uint8Array(authenticatorData.length + clientDataHash.length);
    signed.set(authenticatorData, 0);
    signed.set(clientDataHash, authenticatorData.length);

    return await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, key, signature, signed);
  } catch {
    return false;
  }
}

export function assertionToTuple(assertion: {
  authenticator_data: string;
  client_data_json: string;
  signature: string;
}): WebAuthnTuple {
  const clientDataJSON = new TextDecoder().decode(fromBase64url(assertion.client_data_json));
  const { r, s } = derToRS(fromBase64url(assertion.signature));
  return {
    authenticatorData: bytesToHex(fromBase64url(assertion.authenticator_data)),
    clientDataJSON,
    challengeIndex: clientDataJSON.indexOf('"challenge":'),
    typeIndex: clientDataJSON.indexOf('"type":'),
    r: toHex(r, { size: 32 }),
    s: toHex(s > secp256r1N >> 1n ? secp256r1N - s : s, { size: 32 }),
  };
}

function derToRS(der: Uint8Array): { r: bigint; s: bigint } {
  let offset = 2;
  const readInt = () => {
    const length = der[offset + 1];
    const value = der.slice(offset + 2, offset + 2 + length);
    offset += 2 + length;
    return hexToBigInt(bytesToHex(value));
  };
  return { r: readInt(), s: readInt() };
}

export function rawToDer(raw: Uint8Array): Uint8Array<ArrayBuffer> {
  const encodeInt = (bytes: Uint8Array) => {
    let start = 0;
    while (start < bytes.length - 1 && bytes[start] === 0) start++;
    const trimmed = bytes.slice(start);
    const padded = trimmed[0] & 0x80 ? [0, ...trimmed] : [...trimmed];
    return [0x02, padded.length, ...padded];
  };
  const body = [...encodeInt(raw.slice(0, 32)), ...encodeInt(raw.slice(32, 64))];
  return new Uint8Array([0x30, body.length, ...body]);
}

function hasPrefixAt(text: string, prefix: string, index: number): boolean {
  return index >= 0 && index <= text.length && text.startsWith(prefix, index);
}
