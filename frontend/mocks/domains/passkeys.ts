import { concatHex, hexToBytes, keccak256, type Hex } from "viem";

import type { Schema } from "@/lib/api/client";
import { actionType, challenge, itemsHash } from "@/lib/signing/adr";
import { fromBase64url, toBase64url } from "@/lib/signing/base64url";

import { enqueueChainEvent } from "../chain";
import {
  newId,
  now,
  type Db,
  type KeyActionRow,
  type PasskeyRow,
  type RegistrationSessionRow,
  type UserRow,
} from "../db";
import { fail, guards, parseUuid, readJson, reply, route } from "../http";
import { activeSigner, latestPasskey, relyingParty, verifyAssertion, type Assertion } from "../signed";
import { virtualPublicKey } from "../virtual-authenticator";
import { confirmationOf } from "./signed-actions";

const invalidRequest = "Некорректный запрос";
const registrationSessionTtlMs = 5 * 60_000;
const keyIdPattern = /^0x[0-9a-fA-F]{64}$/;
const base64urlPattern = /^[A-Za-z0-9_-]*$/;

const invalidRequestError = () => fail(400, "INVALID_REQUEST", invalidRequest);
const noOrganization = () => fail(403, "NO_ORGANIZATION", "Пользователь не принадлежит организации");
const passkeyExists = () => fail(409, "PASSKEY_EXISTS", "Ключ уже зарегистрирован");
const invalidRegistration = () => fail(400, "INVALID_REGISTRATION", "Некорректная регистрация passkey");
const passkeyNotFound = () => fail(404, "PASSKEY_NOT_FOUND", "Ключ не найден");
const signerKeyNotActive = () => fail(409, "SIGNER_KEY_NOT_ACTIVE", "Ключ администратора не активен в реестре");
const targetNotPending = () => fail(409, "TARGET_NOT_PENDING", "Ключ оператора не ожидает подтверждения");
const targetNotActive = () => fail(409, "TARGET_NOT_ACTIVE", "Ключ оператора не активен в реестре");
const actionInProgress = () => fail(409, "ACTION_IN_PROGRESS", "Для ключа уже есть действие в работе");
const actionNotFound = () => fail(404, "ACTION_NOT_FOUND", "Действие не найдено");
const actionAlreadySubmitted = () => fail(409, "ACTION_ALREADY_SUBMITTED", "Действие уже отправлено");

type Member = { userId: string; orgId: string; orgName: string };

const authorize = (state: Db, user: UserRow): Member => {
  if (!user.org_id) throw noOrganization();
  return { userId: user.user_id, orgId: user.org_id, orgName: state.organizations.find((org) => org.org_id === user.org_id)?.name ?? "" };
};

const ensureNoLiveKey = (state: Db, userId: string) => {
  const key = latestPasskey(state, userId);
  if (key && (key.status === "PENDING" || key.status === "ACTIVE")) throw passkeyExists();
};

const rfc3339 = (value: string) => value.replace(/\.\d{3}Z$/, "Z");

const passkeyView = (row: PasskeyRow): Schema<"PasskeyView"> => ({
  credential_id: row.credential_id,
  key_id: row.key_id,
  qx: row.qx,
  qy: row.qy,
  role: row.role,
  status: row.status,
  created_at: rfc3339(row.created_at),
});

const bySubmittedDesc = (a: KeyActionRow, b: KeyActionRow) => (a.submitted_at! < b.submitted_at! ? 1 : -1);

const latestConfirmation = (state: Db, orgId: string, credentialId: string): Schema<"KeyConfirmation"> | null => {
  const action = state.key_actions
    .filter((row) => row.org_id === orgId && row.target_credential_id === credentialId && row.submitted_at !== null)
    .sort(bySubmittedDesc)[0];
  return action ? { action_id: action.action_id, action_type: action.action_type, ...confirmationOf(state, action.action_id) } : null;
};

const requireNoConfirmation = (state: Db, orgId: string, credentialId: string, blocking: Schema<"KeyConfirmation">["state"][]) => {
  const confirmation = latestConfirmation(state, orgId, credentialId);
  if (confirmation && blocking.includes(confirmation.state)) throw actionInProgress();
};

const operatorTarget = (state: Db, orgId: string, value: unknown): PasskeyRow => {
  if (typeof value !== "string" || !keyIdPattern.test(value)) throw invalidRequestError();
  const target = state.passkey_credentials.find((key) => key.key_id.toLowerCase() === value.toLowerCase());
  if (!target || target.org_id !== orgId || target.role !== "OPERATOR") throw passkeyNotFound();
  return target;
};

const pendingOperatorTarget = (state: Db, orgId: string, value: unknown): PasskeyRow => {
  const target = operatorTarget(state, orgId, value);
  if (target.status !== "PENDING") throw targetNotPending();
  requireNoConfirmation(state, orgId, target.credential_id, ["SENT", "APPLIED"]);
  return target;
};

const activeOperatorTarget = (state: Db, orgId: string, value: unknown): PasskeyRow => {
  const target = operatorTarget(state, orgId, value);
  if (target.status !== "ACTIVE") throw targetNotActive();
  requireNoConfirmation(state, orgId, target.credential_id, ["SENT"]);
  return target;
};

const adminSigner = (state: Db, userId: string): PasskeyRow => {
  const signer = latestPasskey(state, userId);
  if (!signer) throw passkeyNotFound();
  return activeSigner(state, userId, "Ключ администратора не активен в реестре");
};

const latestOrgPasskey = (state: Db, userId: string, orgId: string): PasskeyRow | undefined =>
  state.passkey_credentials.filter((key) => key.user_id === userId && key.org_id === orgId).sort((a, b) => b.created_at.localeCompare(a.created_at))[0];

const takeSession = (state: Db, userId: string): string => {
  const index = state.registration_sessions.findIndex((row) => row.user_id === userId);
  if (index < 0) throw invalidRegistration();
  const session = state.registration_sessions[index];
  state.registration_sessions.splice(index, 1);
  if (session.expires_at <= now()) throw invalidRegistration();
  return session.challenge;
};

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);

const isBase64url = (value: unknown): value is string => typeof value === "string" && base64urlPattern.test(value) && value.length % 4 !== 1;

const registrationCredential = (body: unknown, sessionChallenge: string): string => {
  if (!isRecord(body) || body.type !== "public-key" || !isBase64url(body.rawId) || body.id !== body.rawId) throw invalidRegistration();
  const response = body.response;
  if (!isRecord(response) || !isBase64url(response.clientDataJSON) || !isBase64url(response.attestationObject)) throw invalidRegistration();
  let clientData: unknown;
  try {
    clientData = JSON.parse(new TextDecoder().decode(fromBase64url(response.clientDataJSON)));
  } catch {
    throw invalidRegistration();
  }
  if (
    !isRecord(clientData) ||
    clientData.type !== "webauthn.create" ||
    clientData.challenge !== sessionChallenge ||
    !relyingParty().origins.includes(clientData.origin as string)
  ) {
    throw invalidRegistration();
  }
  if (new TextDecoder().decode(fromBase64url(response.attestationObject)) !== `virtual:${body.rawId}`) throw invalidRegistration();
  return body.rawId;
};

const insertPasskey = (state: Db, user: UserRow, credentialId: string): Schema<"PasskeyView"> => {
  const key = virtualPublicKey(credentialId);
  if (!key) throw invalidRegistration();
  if (state.passkey_credentials.some((row) => row.credential_id === credentialId)) throw passkeyExists();
  const row: PasskeyRow = {
    credential_id: credentialId,
    user_id: user.user_id,
    org_id: user.org_id!,
    qx: key.qx,
    qy: key.qy,
    key_id: keccak256(concatHex([key.qx, key.qy])),
    role: user.role as PasskeyRow["role"],
    status: "PENDING",
    created_at: now(),
    updated_at: now(),
  };
  state.passkey_credentials.push(row);
  return passkeyView(row);
};

const draftKeyAction = async (
  state: Db,
  user: UserRow,
  request: Request,
  type: KeyActionRow["action_type"],
  targetOf: (state: Db, orgId: string, value: unknown) => PasskeyRow,
): Promise<Schema<"KeyActionDraft">> => {
  const body = await readJson(request, { target_key_id: "string" }, invalidRequest, { allowUnknown: true });
  const member = authorize(state, user);
  const signer = adminSigner(state, user.user_id);
  const target = targetOf(state, member.orgId, body.target_key_id ?? "");
  const actionId = newId();
  const detailsHash = keccak256(concatHex([target.qx as Hex, target.qy as Hex]));
  const actionChallenge = challenge(actionId, type, itemsHash([]), detailsHash);
  state.key_actions.push({
    action_id: actionId,
    org_id: member.orgId,
    action_type: type,
    target_credential_id: target.credential_id,
    signer_credential_id: signer.credential_id,
    challenge: actionChallenge,
    created_by: user.user_id,
    created_at: now(),
    submitted_at: null,
  });
  return {
    action_id: actionId,
    action_type: type,
    item_ids: [],
    subject_key: { qx: target.qx, qy: target.qy },
    details_hash: detailsHash,
    signer_key_id: signer.key_id,
    challenge: toBase64url(hexToBytes(actionChallenge)),
    rp_id: relyingParty().rpId,
    allow_credentials: [{ type: "public-key", id: signer.credential_id }],
    user_verification: "required",
    timeout: 60000,
  };
};

export const passkeysHandlers = [
  route("post", "/passkeys/registration/begin", guards.adminOrOperator, ({ db, user }) => {
    authorize(db, user);
    ensureNoLiveKey(db, user.user_id);
    const session: RegistrationSessionRow = {
      user_id: user.user_id,
      challenge: toBase64url(crypto.getRandomValues(new Uint8Array(32))),
      expires_at: new Date(Date.now() + registrationSessionTtlMs).toISOString(),
    };
    db.registration_sessions = db.registration_sessions.filter((row) => row.user_id !== user.user_id);
    db.registration_sessions.push(session);
    return {
      publicKey: {
        rp: { id: relyingParty().rpId, name: "Avalanche WMS" },
        user: {
          id: toBase64url(hexToBytes(`0x${user.user_id.replaceAll("-", "")}`)),
          name: user.username,
          displayName: user.username,
        },
        challenge: session.challenge,
        pubKeyCredParams: [{ type: "public-key", alg: -7 }],
        timeout: 300000,
        authenticatorSelection: { residentKey: "preferred", userVerification: "required" },
        attestation: "none",
      },
    } satisfies Schema<"PasskeyCreationOptions">;
  }),

  route("post", "/passkeys/registration/finish", guards.adminOrOperator, async ({ db, user, request }) => {
    authorize(db, user);
    ensureNoLiveKey(db, user.user_id);
    const sessionChallenge = takeSession(db, user.user_id);
    const credentialId = registrationCredential(await request.json().catch(() => undefined), sessionChallenge);
    return reply(insertPasskey(db, user, credentialId), 201);
  }),

  route("get", "/passkeys/me", guards.adminOrOperator, ({ db, user }) => {
    const key = latestPasskey(db, user.user_id);
    return key ? passkeyView(key) : null;
  }),

  route("get", "/passkeys/organization", guards.admin, ({ db, user }) => {
    const member = authorize(db, user);
    const members = db.users
      .filter((row) => row.org_id === member.orgId && (row.role === "ADMIN" || row.role === "OPERATOR"))
      .sort((a, b) => (a.username < b.username ? -1 : 1));
    return {
      organization: { org_id: member.orgId, name: member.orgName },
      members: members.map((row) => {
        const key = latestOrgPasskey(db, row.user_id, member.orgId);
        return {
          user_id: row.user_id,
          username: row.username,
          role: row.role,
          passkey: key ? passkeyView(key) : null,
          confirmation: key ? latestConfirmation(db, member.orgId, key.credential_id) : null,
        };
      }),
    } satisfies Schema<"OrganizationOverview">;
  }),

  route("get", "/passkeys/organization/admin-key", guards.admin, ({ db, user }) => {
    const member = authorize(db, user);
    const key = latestPasskey(db, user.user_id);
    if (!key || (key.status !== "PENDING" && key.status !== "ACTIVE")) throw passkeyNotFound();
    return { orgId: member.orgId, name: member.orgName, qx: key.qx, qy: key.qy } satisfies Schema<"AdminKeyExport">;
  }),

  route("post", "/passkeys/organization/keys/reject", guards.admin, async ({ db, user, request }) => {
    const body = await readJson(request, { target_key_id: "string" }, invalidRequest, { allowUnknown: true });
    const member = authorize(db, user);
    const target = pendingOperatorTarget(db, member.orgId, body.target_key_id ?? "");
    target.status = "REJECTED";
    return null;
  }),

  route("post", "/passkeys/actions/key-register/draft", guards.admin, ({ db, user, request }) =>
    draftKeyAction(db, user, request, actionType.KeyRegister, pendingOperatorTarget),
  ),

  route("post", "/passkeys/actions/key-revoke/draft", guards.admin, ({ db, user, request }) =>
    draftKeyAction(db, user, request, actionType.KeyRevoke, activeOperatorTarget),
  ),

  route("post", "/passkeys/actions/:actionId/submit", guards.admin, async ({ db, user, request, params }) => {
    const actionId = parseUuid(params.actionId);
    if (!actionId) throw invalidRequestError();
    const body = await readJson(
      request,
      { credential_id: "string", authenticator_data: "string", client_data_json: "string", signature: "string" },
      invalidRequest,
      { allowUnknown: true },
    );
    const assertion: Assertion = {
      credential_id: body.credential_id ?? "",
      authenticator_data: body.authenticator_data ?? "",
      client_data_json: body.client_data_json ?? "",
      signature: body.signature ?? "",
    };
    for (const field of [assertion.credential_id, assertion.authenticator_data, assertion.client_data_json, assertion.signature]) {
      if (!base64urlPattern.test(field) || field.length % 4 === 1) throw invalidRequestError();
    }
    authorize(db, user);
    const action = db.key_actions.find((row) => row.action_id === actionId);
    if (!action || action.created_by !== user.user_id) throw actionNotFound();
    if (action.submitted_at !== null) throw actionAlreadySubmitted();
    const signer = db.passkey_credentials.find((key) => key.credential_id === action.signer_credential_id);
    if (!signer) throw passkeyNotFound();
    const target = db.passkey_credentials.find((key) => key.credential_id === action.target_credential_id);
    if (!target) throw passkeyNotFound();
    await verifyAssertion(assertion, action.challenge as Hex, signer);
    if (action.action_type === 5 && target.status !== "PENDING") throw targetNotPending();
    if (action.action_type === 6 && target.status !== "ACTIVE") throw targetNotActive();
    const last = db.key_actions
      .filter((row) => row.target_credential_id === target.credential_id && row.submitted_at !== null)
      .sort(bySubmittedDesc)[0];
    if (last) {
      const { state } = confirmationOf(db, last.action_id);
      if (state === "SENT" || (state === "APPLIED" && (action.action_type === 5 || last.action_type === 6))) throw actionInProgress();
    }
    if (signer.status !== "ACTIVE") throw signerKeyNotActive();
    const submittedAt = now();
    db.outbox_events.push({
      event_id: actionId,
      aggregate_id: actionId,
      aggregate_type: "signed_action",
      event_type: "wms.action.v1",
      payload_hash: action.challenge.slice(2),
      payload: {
        version: 1,
        actionId: action.action_id,
        actionType: action.action_type,
        itemIds: [],
        subjectKey: { qx: target.qx, qy: target.qy },
        keyId: signer.key_id,
        webauthn: assertion,
      },
      created_at: submittedAt,
    });
    enqueueChainEvent(db, actionId, "signed_action");
    action.submitted_at = submittedAt;
    return { action_id: actionId, action_type: action.action_type, state: "SENT", reject_reason: null } satisfies Schema<"KeyConfirmation">;
  }),
];
