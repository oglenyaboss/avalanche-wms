import { getAssertion, toRequestOptions } from '@/shared/lib'

import type { PartAssertion, SignedOperationDraft } from './types'

// Signs the draft part by part, in draft order: every part is its own
// authenticator touch (one getAssertion per part), and the resulting assertion
// is tagged with the part's action id for the submit body.
export async function signOperation(
  draft: SignedOperationDraft,
): Promise<PartAssertion[]> {
  const assertions: PartAssertion[] = []

  for (const part of draft.parts) {
    const assertion = await getAssertion(
      toRequestOptions({
        challenge: part.challenge,
        rpId: draft.rpId,
        allowCredentials: draft.allowCredentials,
        userVerification: draft.userVerification,
        timeout: draft.timeout,
      }),
    )
    assertions.push({ ...assertion, actionId: part.actionId })
  }

  return assertions
}
