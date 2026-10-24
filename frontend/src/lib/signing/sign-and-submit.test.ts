import { hexToBytes, type Hex } from "viem";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { installVirtualAuthenticator, resetVirtualAuthenticator, setAuthenticatorMode, virtualPublicKey } from "@mocks/virtual-authenticator";
import { seededKeys } from "@mocks/data/keys";

import { actionType, challenge, itemId, itemsHash } from "./adr";
import { fromBase64url, toBase64url } from "./base64url";
import { signAndSubmit, type SignedDraft, type SignedSubmit } from "./sign-and-submit";
import { assertionToTuple, verifyWebAuthn } from "./webauthn-verify";
import { createPasskey, getAssertion, SigningError } from "./webauthn";

const operator = seededKeys.find((key) => key.username === "operator")!;
const detailsHashHex: Hex = `0x${"11".repeat(32)}`;

function part(actionId: string, productIds: string[]) {
  const challengeHex = challenge(actionId, actionType.PutAway, itemsHash(productIds.map(itemId).sort((a, b) => (a < b ? -1 : 1))), detailsHashHex);
  return { action_id: actionId, item_ids: productIds, details_hash: detailsHashHex, challenge: toBase64url(hexToBytes(challengeHex)), challengeHex };
}

const parts = [
  part("01929c5e-6f00-7000-8000-000000002001", ["55555555-5555-5555-5555-555555555555"]),
  part("01929c5e-6f00-7000-8000-000000002002", ["55555555-5555-5555-5555-555555555556"]),
];

const draft: SignedDraft = {
  operation_id: "01929c5e-6f00-7000-8000-000000002000",
  action_type: 2,
  signer_key_id: "0x00",
  rp_id: "localhost",
  allow_credentials: [{ type: "public-key", id: operator.credentialId }],
  user_verification: "required",
  timeout: 60000,
  parts: parts.map((p) => ({ action_id: p.action_id, item_ids: p.item_ids, details_hash: p.details_hash, challenge: p.challenge })),
};

async function verified(body: SignedSubmit, credentialId: string): Promise<boolean[]> {
  const key = virtualPublicKey(credentialId)!;
  return Promise.all(
    body.assertions.map((a, i) => verifyWebAuthn(assertionToTuple(a), parts[i].challengeHex, key.qx, key.qy)),
  );
}

beforeEach(() => {
  localStorage.clear();
  resetVirtualAuthenticator();
  installVirtualAuthenticator();
});

describe("signAndSubmit на виртуальном аутентификаторе", () => {
  it("подписывает каждую часть и отдаёт результат submit", async () => {
    const submit = vi.fn(async (body: SignedSubmit) => ({ operation_id: body.operation_id }));
    const result = await signAndSubmit(draft, submit);

    expect(result).toEqual({ operation_id: draft.operation_id });
    const body = submit.mock.calls[0][0];
    expect(body.operation_id).toBe(draft.operation_id);
    expect(body.assertions.map((a) => [a.action_id, a.credential_id])).toEqual([
      [parts[0].action_id, operator.credentialId],
      [parts[1].action_id, operator.credentialId],
    ]);
    expect(await verified(body, operator.credentialId)).toEqual([true, true]);
  });

  it("отмена подтверждения — SIGNING_CANCELLED, submit не вызывается", async () => {
    setAuthenticatorMode("cancel");
    const submit = vi.fn();
    const error = await signAndSubmit(draft, submit).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(SigningError);
    expect((error as SigningError).code).toBe("SIGNING_CANCELLED");
    expect(submit).not.toHaveBeenCalled();
  });

  it("режим corrupt даёт подпись, которая не проходит проверку", async () => {
    setAuthenticatorMode("corrupt");
    const submit = vi.fn(async (body: SignedSubmit) => body);
    const body = await signAndSubmit(draft, submit);
    expect(await verified(body, operator.credentialId)).toEqual([false, false]);
  });

  it("ключа нет на устройстве — SIGNING_CANCELLED", async () => {
    const error = await getAssertion({ ...draft, challenge: parts[0].challenge, allow_credentials: [{ type: "public-key", id: "AAAA" }] }).catch(
      (e: unknown) => e,
    );
    expect((error as SigningError).code).toBe("SIGNING_CANCELLED");
  });
});

describe("createPasskey на виртуальном аутентификаторе", () => {
  it("новым ключом можно подписать, подпись проверяется его публичным ключом", async () => {
    const registration = await createPasskey({
      publicKey: {
        rp: { id: "localhost", name: "WMS" },
        user: { id: toBase64url(new TextEncoder().encode("user-1")), name: "op", displayName: "op" },
        challenge: toBase64url(new Uint8Array(32)),
        pubKeyCredParams: [{ type: "public-key", alg: -7 }],
        timeout: 60000,
        authenticatorSelection: { residentKey: "preferred", userVerification: "required" },
        attestation: "none",
      },
    });
    expect(registration.rawId).toBe(registration.id);
    expect(JSON.parse(new TextDecoder().decode(fromBase64url(registration.response.clientDataJSON))).type).toBe("webauthn.create");

    const assertion = await getAssertion({ ...draft, challenge: parts[0].challenge, allow_credentials: [{ type: "public-key", id: registration.rawId }] });
    const key = virtualPublicKey(registration.rawId)!;
    expect(await verifyWebAuthn(assertionToTuple(assertion), parts[0].challengeHex, key.qx, key.qy)).toBe(true);
  });
});
