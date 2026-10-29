import { concatHex, keccak256, type Hex } from "viem";

import type { PasskeyRow, UserRow } from "../../db";
import { ago, combineFixtures, idGroup, mockId, seedIds, type Fixture } from "../fixture-kit";

const keyMaterial = (credentialId: string, qx: Hex, qy: Hex) => ({ credentialId, qx, qy, keyId: keccak256(concatHex([qx, qy])) });

export const passkeysIds = {
  users: { operator3: mockId(idGroup.user, 501), operator4: mockId(idGroup.user, 502) },
  keys: {
    operator3: keyMaterial("cGFzc2tleS1vcGVyYXRvcjM", `0x${"a3".repeat(32)}`, `0x${"b3".repeat(32)}`),
    operator4: keyMaterial("cGFzc2tleS1vcGVyYXRvcjQ", `0x${"a4".repeat(32)}`, `0x${"b4".repeat(32)}`),
  },
};

const operatorUser = (user_id: string, username: string): UserRow => ({
  user_id,
  username,
  password: username,
  role: "OPERATOR",
  is_active: true,
  org_id: seedIds.org,
  created_at: ago(1440),
});

const passkeyRow = (user_id: string, key: (typeof passkeysIds.keys)["operator3"], status: PasskeyRow["status"]): PasskeyRow => ({
  credential_id: key.credentialId,
  user_id,
  org_id: seedIds.org,
  qx: key.qx,
  qy: key.qy,
  key_id: key.keyId,
  role: "OPERATOR",
  status,
  created_at: ago(60),
  updated_at: ago(60),
});

export const passkeysFixtures = (): Fixture =>
  combineFixtures({
    users: [operatorUser(passkeysIds.users.operator3, "operator3"), operatorUser(passkeysIds.users.operator4, "operator4")],
    passkey_credentials: [
      passkeyRow(passkeysIds.users.operator3, passkeysIds.keys.operator3, "PENDING"),
      passkeyRow(passkeysIds.users.operator4, passkeysIds.keys.operator4, "ACTIVE"),
    ],
  });
