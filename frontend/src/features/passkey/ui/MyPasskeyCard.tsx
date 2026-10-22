import { Alert, AlertDescription, Button, Card, CardContent, CardHeader, CardTitle } from '@/shared/ui'
import { formatDateTime } from '@/shared/lib'

import { passkeyErrorText } from '../model/errors'
import { shortKeyId, pendingKeyHint } from '../model/status'
import { useMyPasskey, useRegisterPasskey } from '../model/usePasskey'
import { PasskeyStatusBadge } from './PasskeyStatusBadge'

export function MyPasskeyCard() {
  const { data: passkey, isPending, isError, error } = useMyPasskey()
  const register = useRegisterPasskey()

  const canRegister =
    passkey === null || passkey?.status === 'REJECTED' || passkey?.status === 'REVOKED'

  return (
    <Card>
      <CardHeader>
        <CardTitle>Текущий ключ</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {isPending ? (
          <p className="text-sm text-muted-foreground">Загрузка…</p>
        ) : isError ? (
          <Alert variant="destructive">
            <AlertDescription>{passkeyErrorText(error)}</AlertDescription>
          </Alert>
        ) : passkey ? (
          <>
            <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
              <dt className="text-muted-foreground">Статус</dt>
              <dd>
                <PasskeyStatusBadge status={passkey.status} />
              </dd>
              <dt className="text-muted-foreground">Key ID</dt>
              <dd className="font-mono" title={passkey.keyId}>
                {shortKeyId(passkey.keyId)}
              </dd>
              <dt className="text-muted-foreground">Создан</dt>
              <dd>{formatDateTime(passkey.createdAt)}</dd>
            </dl>
            {passkey.status === 'PENDING' && (
              <p className="text-sm text-muted-foreground">{pendingKeyHint(passkey.role)}</p>
            )}
          </>
        ) : (
          <p className="text-sm text-muted-foreground">Ключа пока нет.</p>
        )}

        {!isPending && !isError && canRegister && (
          <div className="flex flex-col gap-3">
            {register.isError && (
              <Alert variant="destructive">
                <AlertDescription>{passkeyErrorText(register.error)}</AlertDescription>
              </Alert>
            )}
            <Button
              className="self-start"
              disabled={register.isPending}
              onClick={() => register.mutate()}
            >
              {register.isPending ? 'Создание…' : 'Создать ключ'}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
