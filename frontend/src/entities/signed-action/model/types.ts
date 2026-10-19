import type { AssertionJSON } from '@/shared/lib'

// Draft of a signed operation (shared by receiving close and putaway): the
// backend splits the action into parts, each signed with its own authenticator
// touch.
// itemIds and detailsHash describe what the part commits to; challenge and the
// request options come prebuilt from the backend.
export interface SignedOperationDraft {
  operationId: string
  actionType: number
  signerKeyId: string
  rpId: string
  allowCredentials: { type: 'public-key'; id: string }[]
  userVerification: 'required'
  timeout: number
  parts: SignedOperationPart[]
}

export interface SignedOperationPart {
  actionId: string
  itemIds: string[]
  detailsHash: string
  challenge: string
}

// An assertion produced for one part, tagged with the part's action id.
export type PartAssertion = AssertionJSON & { actionId: string }

export type OperationPartState = 'SENT' | 'APPLIED' | 'REJECTED' | 'FAILED'

// Polling result for a signed operation: how the chain treated each part.
export interface OperationStatus {
  operationId: string
  parts: { actionId: string; state: OperationPartState; rejectReason: number | null }[]
}
