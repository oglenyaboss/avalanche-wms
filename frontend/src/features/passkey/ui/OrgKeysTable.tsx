import { Button } from '@/shared/ui'

import type { OrgMember } from '../model/types'
import { confirmationText, shortKeyId } from '../model/status'
import { PasskeyStatusBadge } from './PasskeyStatusBadge'

interface OrgKeysTableProps {
  members: OrgMember[]
  onConfirm: (keyId: string) => void
  confirmingKeyId: string | null
  onReject: (keyId: string) => void
  rejectingKeyId: string | null
}

interface ConfirmationCellProps {
  member: OrgMember
  onConfirm: (keyId: string) => void
  confirmingKeyId: string | null
  onReject: (keyId: string) => void
  rejectingKeyId: string | null
}

const ROLE_LABELS: Record<OrgMember['role'], string> = {
  ADMIN: 'Администратор',
  OPERATOR: 'Оператор',
}

function ConfirmationCell({ member, onConfirm, confirmingKeyId, onReject, rejectingKeyId }: ConfirmationCellProps) {
  const passkey = member.passkey
  if (member.role !== 'OPERATOR' || passkey?.status !== 'PENDING') {
    return <span>—</span>
  }
  if (member.confirmation?.state === 'SENT' || member.confirmation?.state === 'APPLIED') {
    return (
      <span className="text-muted-foreground">{confirmationText(member.confirmation)}</span>
    )
  }
  const busy = confirmingKeyId === passkey.keyId || rejectingKeyId === passkey.keyId
  return (
    <div className="flex flex-col items-start gap-1">
      <div className="flex gap-2">
        <Button
          className="self-start"
          disabled={busy}
          onClick={() => onConfirm(passkey.keyId)}
        >
          Подтвердить ключ
        </Button>
        <Button
          variant="destructive"
          disabled={busy}
          onClick={() => onReject(passkey.keyId)}
        >
          Отклонить
        </Button>
      </div>
      {member.confirmation && (
        <span
          className={
            member.confirmation.state === 'REJECTED' || member.confirmation.state === 'FAILED'
              ? 'text-xs text-destructive'
              : 'text-xs text-muted-foreground'
          }
        >
          {confirmationText(member.confirmation)}
        </span>
      )}
    </div>
  )
}

export function OrgKeysTable({ members, onConfirm, confirmingKeyId, onReject, rejectingKeyId }: OrgKeysTableProps) {
  if (members.length === 0) {
    return <p className="text-sm text-muted-foreground">В организации нет сотрудников.</p>
  }

  return (
    <div className="overflow-x-auto rounded-2xl ring-1 ring-foreground/10">
      <table className="w-full text-left text-sm">
        <thead className="bg-muted/50 text-xs text-muted-foreground">
          <tr>
            <th className="px-4 py-3 font-medium">Пользователь</th>
            <th className="px-4 py-3 font-medium">Роль</th>
            <th className="px-4 py-3 font-medium">Статус ключа</th>
            <th className="px-4 py-3 font-medium">Key ID</th>
            <th className="px-4 py-3 font-medium">Подтверждение</th>
          </tr>
        </thead>
        <tbody>
          {members.map((m) => (
            <tr key={m.userId} className="border-t border-border">
              <td className="px-4 py-3 font-medium">{m.username}</td>
              <td className="px-4 py-3 text-muted-foreground">{ROLE_LABELS[m.role]}</td>
              <td className="px-4 py-3">
                {m.passkey ? (
                  <PasskeyStatusBadge status={m.passkey.status} />
                ) : (
                  <span className="text-muted-foreground">Нет ключа</span>
                )}
              </td>
              <td className="px-4 py-3 font-mono" title={m.passkey?.keyId}>
                {m.passkey ? shortKeyId(m.passkey.keyId) : '—'}
              </td>
              <td className="px-4 py-3">
                <ConfirmationCell
                  member={m}
                  onConfirm={onConfirm}
                  confirmingKeyId={confirmingKeyId}
                  onReject={onReject}
                  rejectingKeyId={rejectingKeyId}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
