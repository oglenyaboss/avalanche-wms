import {
  AdminKeyDownloadButton,
  OrgKeysTable,
  passkeyErrorText,
  useOrganizationKeys,
} from '@/features/passkey'
import { Alert, AlertDescription } from '@/shared/ui'

export function OrgKeysPage() {
  const { data, isPending, isError, error } = useOrganizationKeys()

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
          <OrgKeysTable members={data.members} />
          <AdminKeyDownloadButton />
        </>
      )}
    </div>
  )
}
