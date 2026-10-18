import type { OrgMember } from '../model/types'
import { shortKeyId } from '../model/status'
import { PasskeyStatusBadge } from './PasskeyStatusBadge'

interface OrgKeysTableProps {
  members: OrgMember[]
}

const ROLE_LABELS: Record<OrgMember['role'], string> = {
  ADMIN: 'Администратор',
  OPERATOR: 'Оператор',
}

export function OrgKeysTable({ members }: OrgKeysTableProps) {
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
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
