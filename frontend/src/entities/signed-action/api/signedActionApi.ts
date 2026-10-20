import { apiClient } from '@/shared/api'

import type {
  OperationPartState,
  OperationStatus,
  PartAssertion,
  SignedOperationDraft,
} from '../model/types'

interface ApiEnvelope<T> {
  success: boolean
  data: T | null
  error: { code: string; message: string } | null
}

function unwrap<T>(envelope: ApiEnvelope<T>): T {
  if (!envelope.success || envelope.data === null) {
    throw new Error(envelope.error?.message ?? 'Некорректный ответ сервера')
  }
  return envelope.data
}

interface SignedOperationPartResponse {
  action_id: string
  item_ids: string[]
  details_hash: string
  challenge: string
}

export interface SignedOperationDraftResponse {
  operation_id: string
  action_type: number
  signer_key_id: string
  rp_id: string
  allow_credentials: { type: 'public-key'; id: string }[]
  user_verification: 'required'
  timeout: number
  parts: SignedOperationPartResponse[]
}

export function mapSignedOperationDraft(
  raw: SignedOperationDraftResponse,
): SignedOperationDraft {
  return {
    operationId: raw.operation_id,
    actionType: raw.action_type,
    signerKeyId: raw.signer_key_id,
    rpId: raw.rp_id,
    allowCredentials: raw.allow_credentials,
    userVerification: raw.user_verification,
    timeout: raw.timeout,
    parts: raw.parts.map((part) => ({
      actionId: part.action_id,
      itemIds: part.item_ids,
      detailsHash: part.details_hash,
      challenge: part.challenge,
    })),
  }
}

// The submit body carries one assertion per part, snake_case like the passkey
// key-confirmation submit.
export function toAssertionsBody(assertions: PartAssertion[]) {
  return assertions.map((assertion) => ({
    action_id: assertion.actionId,
    credential_id: assertion.credentialId,
    authenticator_data: assertion.authenticatorData,
    client_data_json: assertion.clientDataJson,
    signature: assertion.signature,
  }))
}

interface OperationPartStatusResponse {
  action_id: string
  state: OperationPartState
  reject_reason: number | null
}

interface OperationStatusResponse {
  operation_id: string
  parts: OperationPartStatusResponse[]
}

export async function getOperationStatus(operationId: string): Promise<OperationStatus> {
  const { data } = await apiClient.get<ApiEnvelope<OperationStatusResponse>>(
    `/signed-actions/operations/${operationId}`,
  )

  const result = unwrap(data)

  return {
    operationId: result.operation_id,
    parts: result.parts.map((part) => ({
      actionId: part.action_id,
      state: part.state,
      rejectReason: part.reject_reason,
    })),
  }
}
