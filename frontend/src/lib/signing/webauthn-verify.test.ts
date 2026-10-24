import { bytesToHex, hexToBigInt, hexToBytes, toHex } from "viem";
import { describe, expect, it } from "vitest";

import { toBase64url } from "./base64url";
import { vectors, type ActionVector } from "./golden-vectors";
import { assertionToTuple, rawToDer, verifyWebAuthn, type WebAuthnTuple } from "./webauthn-verify";

const n = 0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551n;
const { qx, qy } = vectors.signer;
const receive = vectors.actions.find((action) => action.name === "receive")!;

function tupleOf(action: ActionVector): WebAuthnTuple {
  const wa = action.webauthn;
  return {
    authenticatorData: wa.authenticatorData,
    clientDataJSON: new TextDecoder().decode(hexToBytes(wa.clientDataJSONHex)),
    challengeIndex: wa.challengeIndex,
    typeIndex: wa.typeIndex,
    r: wa.r,
    s: wa.s,
  };
}

describe("verifyWebAuthn по golden vectors", () => {
  it("каждое assertion проходит с ключом подписанта", async () => {
    for (const action of vectors.actions) {
      expect(await verifyWebAuthn(tupleOf(action), action.challenge, qx, qy), action.name).toBe(true);
    }
  });

  it("чужой challenge отклоняется", async () => {
    const changed = hexToBytes(receive.challenge);
    changed[31] ^= 1;
    expect(await verifyWebAuthn(tupleOf(receive), bytesToHex(changed), qx, qy)).toBe(false);
  });

  it("high-s отклоняется", async () => {
    const tuple = tupleOf(receive);
    const highS = toHex(n - hexToBigInt(tuple.s), { size: 32 });
    expect(await verifyWebAuthn({ ...tuple, s: highS }, receive.challenge, qx, qy)).toBe(false);
  });

  it("без флага UV отклоняется", async () => {
    const tuple = tupleOf(receive);
    const data = hexToBytes(tuple.authenticatorData);
    data[32] &= ~0x04;
    expect(await verifyWebAuthn({ ...tuple, authenticatorData: bytesToHex(data) }, receive.challenge, qx, qy)).toBe(false);
  });

  it("испорченный r отклоняется", async () => {
    const tuple = tupleOf(receive);
    const r = hexToBytes(tuple.r);
    r[0] ^= 1;
    expect(await verifyWebAuthn({ ...tuple, r: bytesToHex(r) }, receive.challenge, qx, qy)).toBe(false);
  });

  it("чужой ключ отклоняется", async () => {
    expect(await verifyWebAuthn(tupleOf(receive), receive.challenge, vectors.subjectKey.qx, qy)).toBe(false);
  });
});

describe("assertionToTuple", () => {
  it("DER-подпись и base64url-поля из браузера сводятся к кортежу векторов", async () => {
    const wa = receive.webauthn;
    const raw = new Uint8Array([...hexToBytes(wa.r), ...hexToBytes(wa.s)]);
    const tuple = assertionToTuple({
      authenticator_data: toBase64url(hexToBytes(wa.authenticatorData)),
      client_data_json: toBase64url(hexToBytes(wa.clientDataJSONHex)),
      signature: toBase64url(rawToDer(raw)),
    });
    expect(tuple).toEqual(tupleOf(receive));
  });

  it("high-s из браузера нормализуется и проходит проверку", async () => {
    const wa = receive.webauthn;
    const highS = hexToBytes(toHex(n - hexToBigInt(wa.s), { size: 32 }));
    const tuple = assertionToTuple({
      authenticator_data: toBase64url(hexToBytes(wa.authenticatorData)),
      client_data_json: toBase64url(hexToBytes(wa.clientDataJSONHex)),
      signature: toBase64url(rawToDer(new Uint8Array([...hexToBytes(wa.r), ...highS]))),
    });
    expect(await verifyWebAuthn(tuple, receive.challenge, qx, qy)).toBe(true);
  });
});
