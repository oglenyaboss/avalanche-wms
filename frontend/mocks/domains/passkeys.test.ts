import { describe, expect, it } from "vitest";
import { bytesToHex, concatHex, hexToBytes, keccak256, type Hex } from "viem";

import { api, unwrap } from "@/lib/api/client";
import type { Schema } from "@/lib/api/client";
import { challenge, itemsHash } from "@/lib/signing/adr";
import { fromBase64url, toBase64url } from "@/lib/signing/base64url";
import { signAndSubmit, type SignedSubmit } from "@/lib/signing/sign-and-submit";
import { createPasskey, getAssertion } from "@/lib/signing/webauthn";

import { setChainMode } from "../chain";
import { db, saveDb } from "../db";
import { passkeysIds } from "../data/fixtures/passkeys";
import { putawayIds } from "../data/fixtures/putaway";
import { seedIds, seededCredentialIds } from "../data/fixture-kit";
import { seededKeys } from "../data/keys";
import { loginAs, setupMockApi, waitForChain } from "../testing";
import { setAuthenticatorMode, virtualPublicKey } from "../virtual-authenticator";

setupMockApi();

const keys = passkeysIds.keys;
const invalidRequestMessage = "Некорректный запрос";
const registrationError = { status: 400, code: "INVALID_REGISTRATION", message: "Некорректная регистрация passkey" };

const begin = () => unwrap(api.POST("/passkeys/registration/begin"));
const finish = (body: Schema<"PasskeyRegistrationResponse">) => api.POST("/passkeys/registration/finish", { body });
const register = async () => finish(await createPasskey(await begin()));
const me = () => unwrap(api.GET("/passkeys/me"));
const organization = () => unwrap(api.GET("/passkeys/organization"));
const adminKeyExport = () => unwrap(api.GET("/passkeys/organization/admin-key"));
const reject = (target_key_id: string) => api.POST("/passkeys/organization/keys/reject", { body: { target_key_id } });
const draftRegister = (target_key_id: string) => unwrap(api.POST("/passkeys/actions/key-register/draft", { body: { target_key_id } }));
const draftRevoke = (target_key_id: string) => unwrap(api.POST("/passkeys/actions/key-revoke/draft", { body: { target_key_id } }));
const submitAction = (action_id: string, body: Schema<"WebAuthnAssertion">) =>
  api.POST("/passkeys/actions/{action_id}/submit", { params: { path: { action_id } }, body });
const signKeyAction = async (draft: Schema<"KeyActionDraft">) => unwrap(submitAction(draft.action_id, await getAssertion(draft)));

const memberOf = async (username: string) => (await organization()).members.find((member) => member.username === username)!;
const passkeyRow = (credentialId: string) => db().passkey_credentials.find((key) => key.credential_id === credentialId)!;
const adminKeyRow = () => passkeyRow(seededCredentialIds.admin);

const putawayDraft = () =>
  unwrap(api.POST("/putaway/scan-storage-bin/draft", { body: { product_ids: [putawayIds.products["QR-PUT-0001"]], storage_bin_id: seedIds.bins["A-01-01"] } }));
const putawaySubmit = (body: SignedSubmit) => unwrap(api.POST("/putaway/scan-storage-bin/submit", { body }));

describe("регистрация ключа", () => {
  it("begin отдаёт опции WebAuthn без существующих ключей", async () => {
    await loginAs("operator2");
    const options = await begin();
    expect(options.publicKey.rp).toEqual({ id: location.hostname, name: "Avalanche WMS" });
    expect(options.publicKey.user).toEqual({
      id: toBase64url(hexToBytes(`0x${seedIds.users.operator2.replaceAll("-", "")}`)),
      name: "operator2",
      displayName: "operator2",
    });
    expect(options.publicKey.pubKeyCredParams).toEqual([{ type: "public-key", alg: -7 }]);
    expect(options.publicKey.timeout).toBe(300000);
    expect(options.publicKey.authenticatorSelection).toEqual({ residentKey: "preferred", userVerification: "required" });
    expect(options.publicKey.attestation).toBe("none");
    expect(options.publicKey.challenge).toHaveLength(43);
    expect(options.publicKey.excludeCredentials).toBeUndefined();
  });

  it("register сохраняет PENDING-ключ под 201 и он же в me", async () => {
    await loginAs("operator2");
    const credential = await createPasskey(await begin());
    const result = await finish(credential);
    expect(result.response.status).toBe(201);
    const view = await unwrap(Promise.resolve(result));
    const { qx, qy } = virtualPublicKey(credential.rawId)!;
    expect(view).toMatchObject({
      credential_id: credential.rawId,
      key_id: keccak256(concatHex([qx, qy])),
      qx,
      qy,
      role: "OPERATOR",
      status: "PENDING",
    });
    expect(view.created_at).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/);
    expect(await me()).toEqual(view);
  });

  it("без ключа me отдаёт null", async () => {
    await loginAs("operator2");
    await expect(me()).resolves.toBeNull();
  });

  it("повторный begin при живом ключе — PASSKEY_EXISTS", async () => {
    await loginAs("operator2");
    await register();
    await expect(begin()).rejects.toMatchObject({ status: 409, code: "PASSKEY_EXISTS", message: "Ключ уже зарегистрирован" });
    await loginAs("operator");
    await expect(begin()).rejects.toMatchObject({ status: 409, code: "PASSKEY_EXISTS", message: "Ключ уже зарегистрирован" });
  });

  it("finish без begin — INVALID_REGISTRATION", async () => {
    await loginAs("operator2");
    await expect(unwrap(finish({} as never))).rejects.toMatchObject(registrationError);
  });

  it("finish с пустым телом после begin — INVALID_REGISTRATION", async () => {
    await loginAs("operator2");
    await begin();
    await expect(unwrap(finish({} as never))).rejects.toMatchObject(registrationError);
  });

  it("finish с подменённым challenge — INVALID_REGISTRATION", async () => {
    await loginAs("operator2");
    const options = await begin();
    const tampered = { publicKey: { ...options.publicKey, challenge: toBase64url(crypto.getRandomValues(new Uint8Array(32))) } };
    await expect(unwrap(finish(await createPasskey(tampered)))).rejects.toMatchObject(registrationError);
  });

  it("finish с просроченной сессией — INVALID_REGISTRATION", async () => {
    await loginAs("operator2");
    const options = await begin();
    const state = db();
    state.registration_sessions.find((row) => row.user_id === seedIds.users.operator2)!.expires_at = new Date(Date.now() - 1000).toISOString();
    saveDb();
    await expect(unwrap(finish(await createPasskey(options)))).rejects.toMatchObject(registrationError);
  });

  it("finish с уже существующим credential_id — PASSKEY_EXISTS", async () => {
    await loginAs("operator2");
    const challengeValue = (await begin()).publicKey.challenge;
    const rawId = seededCredentialIds.operator;
    const body = {
      id: rawId,
      rawId,
      type: "public-key",
      response: {
        clientDataJSON: toBase64url(new TextEncoder().encode(JSON.stringify({ type: "webauthn.create", challenge: challengeValue, origin: location.origin }))),
        attestationObject: toBase64url(new TextEncoder().encode(`virtual:${rawId}`)),
      },
    };
    await expect(unwrap(finish(body as never))).rejects.toMatchObject({ status: 409, code: "PASSKEY_EXISTS", message: "Ключ уже зарегистрирован" });
  });

  it("auditor — FORBIDDEN", async () => {
    await loginAs("auditor");
    await expect(begin()).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Требуется роль администратора или оператора" });
  });

  it("пользователь без организации — NO_ORGANIZATION", async () => {
    await loginAs("operator2");
    const state = db();
    state.users.find((row) => row.user_id === seedIds.users.operator2)!.org_id = null;
    saveDb();
    await expect(begin()).rejects.toMatchObject({ status: 403, code: "NO_ORGANIZATION", message: "Пользователь не принадлежит организации" });
  });
});

describe("организация", () => {
  it("участники по username с ключами и подтверждениями", async () => {
    await loginAs("admin");
    const result = await organization();
    expect(result.organization).toEqual({ org_id: seedIds.org, name: "Демо-склад" });
    expect(result.members.map((member) => member.username)).toEqual(["admin", "operator", "operator2", "operator3", "operator4"]);
    const byName = new Map(result.members.map((member) => [member.username, member]));
    expect(byName.get("admin")!.passkey).toMatchObject({ role: "ADMIN", status: "ACTIVE" });
    expect(byName.get("operator2")!.passkey).toBeNull();
    expect(byName.get("operator2")!.confirmation).toBeNull();
    expect(byName.get("operator3")!.passkey).toMatchObject({ status: "PENDING" });
  });

  it("operator — FORBIDDEN", async () => {
    await loginAs("operator");
    await expect(organization()).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Только администратор может выполнять это действие" });
  });

  it("admin-key отдаёт публичный ключ админа, REVOKED — 404", async () => {
    await loginAs("admin");
    const adminSeed = seededKeys.find((key) => key.username === "admin")!;
    expect(await adminKeyExport()).toEqual({
      orgId: seedIds.org,
      name: "Демо-склад",
      qx: bytesToHex(fromBase64url(adminSeed.jwk.x!)),
      qy: bytesToHex(fromBase64url(adminSeed.jwk.y!)),
    });
    adminKeyRow().status = "REVOKED";
    saveDb();
    await expect(adminKeyExport()).rejects.toMatchObject({ status: 404, code: "PASSKEY_NOT_FOUND", message: "Ключ не найден" });
  });
});

describe("отклонение ключа", () => {
  it("reject ставит REJECTED и освобождает регистрацию", async () => {
    await loginAs("admin");
    await expect(unwrap(reject(keys.operator3.keyId))).resolves.toBeNull();
    expect(passkeyRow(keys.operator3.credentialId).status).toBe("REJECTED");
    await loginAs("operator3");
    await expect(begin()).resolves.toBeTruthy();
  });

  it("reject принимает ВЕРХНИЙ регистр hex и лишние поля", async () => {
    await loginAs("admin");
    const keyId = keys.operator3.keyId;
    const request = api.POST("/passkeys/organization/keys/reject", { body: { target_key_id: `0x${keyId.slice(2).toUpperCase()}`, extra: 1 } as never });
    await expect(unwrap(request)).resolves.toBeNull();
    expect(passkeyRow(keys.operator3.credentialId).status).toBe("REJECTED");
  });

  it.each([
    ["не hex", "abc", 400, "INVALID_REQUEST", invalidRequestMessage],
    ["неизвестный keyId", `0x${"0".repeat(64)}`, 404, "PASSKEY_NOT_FOUND", "Ключ не найден"],
    ["ключ operator4", keys.operator4.keyId, 409, "TARGET_NOT_PENDING", "Ключ оператора не ожидает подтверждения"],
  ])("reject: %s — %s", async (_, keyId, status, code, message) => {
    await loginAs("admin");
    await expect(unwrap(reject(keyId))).rejects.toMatchObject({ status, code, message });
  });

  it("reject ключа админа — PASSKEY_NOT_FOUND", async () => {
    await loginAs("admin");
    await expect(unwrap(reject(adminKeyRow().key_id))).rejects.toMatchObject({ status: 404, code: "PASSKEY_NOT_FOUND", message: "Ключ не найден" });
  });
});

describe("подтверждение ключа (KeyRegister)", () => {
  it("draft register отдаёт материалы для подписи админом", async () => {
    await loginAs("admin");
    const draft = await draftRegister(keys.operator3.keyId);
    expect(draft.action_type).toBe(5);
    expect(draft.item_ids).toEqual([]);
    expect(draft.subject_key).toEqual({ qx: keys.operator3.qx, qy: keys.operator3.qy });
    expect(draft.details_hash).toBe(keys.operator3.keyId);
    expect(draft.signer_key_id).toBe(adminKeyRow().key_id);
    expect(draft.rp_id).toBe(location.hostname);
    expect(draft.allow_credentials).toEqual([{ type: "public-key", id: seededCredentialIds.admin }]);
    expect(draft.user_verification).toBe("required");
    expect(draft.timeout).toBe(60000);
    expect(draft.challenge).toBe(toBase64url(hexToBytes(challenge(draft.action_id, 5, itemsHash([]), keys.operator3.keyId as Hex))));
  });

  it("submit register → SENT, после цепи APPLIED и ключ ACTIVE", async () => {
    await loginAs("admin");
    const draft = await draftRegister(keys.operator3.keyId);
    await expect(signKeyAction(draft)).resolves.toEqual({ action_id: draft.action_id, action_type: 5, state: "SENT", reject_reason: null });
    expect((await memberOf("operator3")).confirmation).toMatchObject({ state: "SENT", reject_reason: null });
    expect((await memberOf("operator3")).passkey).toMatchObject({ status: "PENDING" });
    await waitForChain();
    expect((await memberOf("operator3")).confirmation).toMatchObject({ state: "APPLIED", reject_reason: null });
    expect((await memberOf("operator3")).passkey).toMatchObject({ status: "ACTIVE" });
  });

  it("повторная отправка, чужой id, битый id и тело — ошибки", async () => {
    await loginAs("admin");
    const draft = await draftRegister(keys.operator3.keyId);
    const body = await getAssertion(draft);
    await expect(unwrap(submitAction(draft.action_id, body))).resolves.toMatchObject({ state: "SENT" });
    await expect(unwrap(submitAction(draft.action_id, body))).rejects.toMatchObject({
      status: 409,
      code: "ACTION_ALREADY_SUBMITTED",
      message: "Действие уже отправлено",
    });
    const junk = { credential_id: "AA", authenticator_data: "AA", client_data_json: "AA", signature: "AA" };
    await expect(unwrap(submitAction("01929c5e-0000-7000-800b-999999999999", junk))).rejects.toMatchObject({
      status: 404,
      code: "ACTION_NOT_FOUND",
      message: "Действие не найдено",
    });
    await expect(unwrap(submitAction("x", body))).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: invalidRequestMessage });
    await expect(unwrap(submitAction(draft.action_id, { ...body, credential_id: "a=" }))).rejects.toMatchObject({
      status: 400,
      code: "INVALID_REQUEST",
      message: invalidRequestMessage,
    });
  });

  it("испорченная подпись — INVALID_ASSERTION, действие остаётся черновиком", async () => {
    await loginAs("admin");
    const draft = await draftRegister(keys.operator3.keyId);
    setAuthenticatorMode("corrupt");
    await expect(signKeyAction(draft)).rejects.toMatchObject({ status: 400, code: "INVALID_ASSERTION", message: "Подтверждение не прошло проверку" });
    setAuthenticatorMode("ok");
    await expect(signKeyAction(draft)).resolves.toMatchObject({ state: "SENT" });
  });

  it("hold: отправленное действие блокирует новые draft и reject", async () => {
    await loginAs("admin");
    setChainMode("hold");
    const draft = await draftRegister(keys.operator3.keyId);
    await expect(signKeyAction(draft)).resolves.toMatchObject({ state: "SENT" });
    await expect(draftRegister(keys.operator3.keyId)).rejects.toMatchObject({
      status: 409,
      code: "ACTION_IN_PROGRESS",
      message: "Для ключа уже есть действие в работе",
    });
    await expect(unwrap(reject(keys.operator3.keyId))).rejects.toMatchObject({ status: 409, code: "ACTION_IN_PROGRESS" });
  });

  it("два черновика при hold: второй submit — ACTION_IN_PROGRESS", async () => {
    await loginAs("admin");
    const first = await draftRegister(keys.operator3.keyId);
    const second = await draftRegister(keys.operator3.keyId);
    setChainMode("hold");
    await expect(signKeyAction(first)).resolves.toMatchObject({ state: "SENT" });
    await expect(signKeyAction(second)).rejects.toMatchObject({ status: 409, code: "ACTION_IN_PROGRESS", message: "Для ключа уже есть действие в работе" });
  });

  it("после применения первого черновика второй submit — TARGET_NOT_PENDING", async () => {
    await loginAs("admin");
    const first = await draftRegister(keys.operator3.keyId);
    const second = await draftRegister(keys.operator3.keyId);
    await expect(signKeyAction(first)).resolves.toMatchObject({ state: "SENT" });
    await waitForChain();
    await expect(signKeyAction(second)).rejects.toMatchObject({
      status: 409,
      code: "TARGET_NOT_PENDING",
      message: "Ключ оператора не ожидает подтверждения",
    });
  });

  it("цепь отклонила KeyRegister — REJECTED, ключ остаётся PENDING", async () => {
    await loginAs("admin");
    setChainMode("reject:4");
    const draft = await draftRegister(keys.operator3.keyId);
    await expect(signKeyAction(draft)).resolves.toMatchObject({ state: "SENT" });
    await waitForChain();
    const member = await memberOf("operator3");
    expect(member.confirmation).toEqual({ action_id: draft.action_id, action_type: 5, state: "REJECTED", reject_reason: 4 });
    expect(member.passkey).toMatchObject({ status: "PENDING" });
    await expect(draftRegister(keys.operator3.keyId)).resolves.toBeTruthy();
    await expect(unwrap(reject(keys.operator3.keyId))).resolves.toBeNull();
  });

  it("у админа нет ключей — PASSKEY_NOT_FOUND до разбора цели", async () => {
    await loginAs("admin");
    const state = db();
    state.passkey_credentials = state.passkey_credentials.filter((key) => key.credential_id !== seededCredentialIds.admin);
    saveDb();
    await expect(draftRegister("abc")).rejects.toMatchObject({ status: 404, code: "PASSKEY_NOT_FOUND", message: "Ключ не найден" });
  });

  it("ключ админа REVOKED — draft не начинается", async () => {
    await loginAs("admin");
    adminKeyRow().status = "REVOKED";
    saveDb();
    await expect(draftRegister(keys.operator3.keyId)).rejects.toMatchObject({
      status: 409,
      code: "SIGNER_KEY_NOT_ACTIVE",
      message: "Ключ администратора не активен в реестре",
    });
  });

  it("ключ админа отозван между draft и submit — SIGNER_KEY_NOT_ACTIVE", async () => {
    await loginAs("admin");
    const draft = await draftRegister(keys.operator3.keyId);
    adminKeyRow().status = "REVOKED";
    saveDb();
    await expect(signKeyAction(draft)).rejects.toMatchObject({
      status: 409,
      code: "SIGNER_KEY_NOT_ACTIVE",
      message: "Ключ администратора не активен в реестре",
    });
  });

  it("чужой черновик — ACTION_NOT_FOUND", async () => {
    await loginAs("admin");
    const draft = await draftRegister(keys.operator3.keyId);
    const state = db();
    state.key_actions.find((row) => row.action_id === draft.action_id)!.created_by = seedIds.users.operator;
    saveDb();
    await expect(signKeyAction(draft)).rejects.toMatchObject({ status: 404, code: "ACTION_NOT_FOUND", message: "Действие не найдено" });
  });

  it("draft только для админа; ACTIVE-цель — TARGET_NOT_PENDING", async () => {
    await loginAs("operator");
    await expect(draftRegister(keys.operator3.keyId)).rejects.toMatchObject({
      status: 403,
      code: "FORBIDDEN",
      message: "Только администратор может выполнять это действие",
    });
    await loginAs("admin");
    await expect(draftRegister(keys.operator4.keyId)).rejects.toMatchObject({
      status: 409,
      code: "TARGET_NOT_PENDING",
      message: "Ключ оператора не ожидает подтверждения",
    });
  });
});

describe("отзыв ключа (KeyRevoke)", () => {
  it("revoke operator4: submit → SENT, после цепи REVOKED и APPLIED", async () => {
    await loginAs("admin");
    const draft = await draftRevoke(keys.operator4.keyId);
    expect(draft.action_type).toBe(6);
    expect(draft.subject_key).toEqual({ qx: keys.operator4.qx, qy: keys.operator4.qy });
    await expect(signKeyAction(draft)).resolves.toMatchObject({ state: "SENT", reject_reason: null });
    await waitForChain();
    expect((await memberOf("operator4")).passkey).toMatchObject({ status: "REVOKED" });
    expect((await memberOf("operator4")).confirmation).toMatchObject({ state: "APPLIED", reject_reason: null });
  });

  it("revoke PENDING-ключа — TARGET_NOT_ACTIVE", async () => {
    await loginAs("admin");
    await expect(draftRevoke(keys.operator3.keyId)).rejects.toMatchObject({
      status: 409,
      code: "TARGET_NOT_ACTIVE",
      message: "Ключ оператора не активен в реестре",
    });
  });

  it("hold: отправленный отзыв блокирует новый revoke", async () => {
    await loginAs("admin");
    setChainMode("hold");
    const first = await draftRevoke(keys.operator4.keyId);
    await expect(signKeyAction(first)).resolves.toMatchObject({ state: "SENT" });
    await expect(draftRevoke(keys.operator4.keyId)).rejects.toMatchObject({
      status: 409,
      code: "ACTION_IN_PROGRESS",
      message: "Для ключа уже есть действие в работе",
    });
  });
});

describe("сквозные сценарии", () => {
  it("оператор зарегистрировал ключ → админ подтвердил → оператор раскладывает товар", async () => {
    await loginAs("operator2");
    const view = await unwrap(register());
    await loginAs("admin");
    await signKeyAction(await draftRegister(view.key_id));
    await waitForChain();
    await loginAs("operator2");
    expect((await me())!.status).toBe("ACTIVE");
    const result = await signAndSubmit(await putawayDraft(), putawaySubmit);
    expect(result.products_placed).toBe(1);
  });

  it("отзыв ключа оператора блокирует его подписи", async () => {
    await loginAs("operator");
    const stale = await putawayDraft();
    await loginAs("admin");
    setChainMode("hold");
    const operatorKeyId = passkeyRow(seededCredentialIds.operator).key_id;
    await signKeyAction(await draftRevoke(operatorKeyId));
    await loginAs("operator");
    await expect(signAndSubmit(stale, putawaySubmit)).rejects.toMatchObject({
      status: 409,
      code: "SIGNER_KEY_NOT_ACTIVE",
      message: "Ключ подписанта не активен в реестре",
    });
  });
});
