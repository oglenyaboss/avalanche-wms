import { readFileSync } from "node:fs";
import { join } from "node:path";

import type { Hex } from "viem";

import type { ActionDetails } from "./adr";

export type ActionVector = {
  name: string;
  actionId: string;
  actionType: number;
  itemIds: Hex[];
  itemsHash: Hex;
  detailsHash: Hex;
  challenge: Hex;
  details: Partial<ActionDetails>;
  webauthn: {
    authenticatorData: Hex;
    clientDataJSONHex: Hex;
    challengeIndex: number;
    typeIndex: number;
    r: Hex;
    s: Hex;
  };
};

export type GoldenVectors = {
  actions: ActionVector[];
  products: { productId: string; itemId: Hex }[];
  signer: { qx: Hex; qy: Hex };
  subjectKey: { qx: Hex; qy: Hex };
};

export const vectors: GoldenVectors = JSON.parse(
  readFileSync(join(process.cwd(), "../testdata/signed-actions/vectors.v1.json"), "utf8"),
);
