import { useState } from 'react'

import {
  AdminKeyDownloadButton,
  OrgKeysTable,
  assertionErrorText,
  passkeyErrorText,
  useConfirmKey,
  useOrganizationKeys,
  useRejectKey,
  useRevokeKey,
} from '@/features/passkey'
import { Alert, AlertDescription } from '@/shared/ui'

import { ConfirmRevokeKeyDialog } from './ConfirmRevokeKeyDialog'

export function OrgKeysPage() {
  const { data, isPending, isError, error } = useOrganizationKeys()
  const confirm = useConfirmKey()
  const reject = useRejectKey()
  const revoke = useRevokeKey()
  const [revokeTargetId, setRevokeTargetId] = useState<string | null>(null)

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6 px-6 py-10">
      <header className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold tracking-tight">
          {data ? `Ключи организации ${data.organization.name}` : 'Ключи организации'}
        </h1>
        <p className="text-muted-foreground">Сотрудники организации и их ключи подписи.</p>
      </header>

      {isPending ? (
        <p className="text-sm text-muted-foreground">Загрузка…</p>
      ) : isError ? (
        <Alert variant="destructive">
          <AlertDescription>{passkeyErrorText(error)}</AlertDescription>
        </Alert>
      ) : (
        <>
          <OrgKeysTable
            members={data.members}
            onConfirm={(keyId) => confirm.mutate(keyId)}
            confirmingKeyId={confirm.isPending ? (confirm.variables ?? null) : null}
            onReject={(keyId) => reject.mutate(keyId)}
            rejectingKeyId={reject.isPending ? (reject.variables ?? null) : null}
            onRevoke={setRevokeTargetId}
            revokingKeyId={revoke.isPending ? (revoke.variables ?? null) : null}
          />
          {confirm.isError && (
            <Alert variant="destructive">
              <AlertDescription>
                {assertionErrorText(confirm.error) ?? passkeyErrorText(confirm.error)}
              </AlertDescription>
            </Alert>
          )}
          {reject.isError && (
            <Alert variant="destructive">
              <AlertDescription>{passkeyErrorText(reject.error)}</AlertDescription>
            </Alert>
          )}
          {revoke.isError && (
            <Alert variant="destructive">
              <AlertDescription>
                {assertionErrorText(revoke.error) ?? passkeyErrorText(revoke.error)}
              </AlertDescription>
            </Alert>
          )}
          <AdminKeyDownloadButton />
          <ConfirmRevokeKeyDialog
            open={revokeTargetId !== null}
            onConfirm={() => {
              if (revokeTargetId !== null) {
                revoke.mutate(revokeTargetId)
              }
              setRevokeTargetId(null)
            }}
            onCancel={() => setRevokeTargetId(null)}
          />
        </>
      )}
    </div>
  )
}
