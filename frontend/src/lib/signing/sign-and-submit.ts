import type { Schema } from "@/lib/api/client";

import { getAssertion } from "./webauthn";

export type SignedDraft = Schema<"SignedActionDraft">;
export type SignedSubmit = Schema<"SignedActionSubmitRequest">;

export async function signAndSubmit<Result>(draft: SignedDraft, submit: (body: SignedSubmit) => Promise<Result>): Promise<Result> {
  const assertions: SignedSubmit["assertions"] = [];
  for (const part of draft.parts) {
    const assertion = await getAssertion({ ...draft, challenge: part.challenge });
    assertions.push({ action_id: part.action_id, ...assertion });
  }
  return submit({ operation_id: draft.operation_id, assertions });
}
