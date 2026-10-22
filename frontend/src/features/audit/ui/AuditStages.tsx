// Per-stage audit result: one row per signed action with ✓/✗ marks.
import { Fragment } from 'react'
import { formatDateTime, truncateHash } from '@/shared/lib'

import type { AuditCheck, AuditStage } from '../model/types'

const STAGE_NAMES: Record<number, string> = {
  1: 'Приёмка',
  2: 'Раскладка',
  3: 'Сборка',
  4: 'Отгрузка',
}

const STATUS_TEXT: Record<AuditStage['status'], string> = {
  applied: 'В цепи',
  rejected: 'Отклонено',
  pending: 'Ожидает записи в цепь',
  missingOnChain: 'Нет в цепи',
  missingInDb: 'Нет в БД',
}

const CHECK_KEYS = [
  { key: 'detailsHash', label: 'Хеш деталей' },
  { key: 'signer', label: 'Подписант' },
  { key: 'items', label: 'Товары' },
  { key: 'signature', label: 'Подпись' },
] as const

function stageName(actionType: number): string {
  return STAGE_NAMES[actionType] ?? `Тип ${actionType}`
}

function statusText(stage: AuditStage): string {
  if (stage.status === 'rejected') return `Отклонено (код ${stage.rejectReason})`
  return STATUS_TEXT[stage.status]
}

function CheckMark({ check, label }: { check: AuditCheck; label: string }) {
  const mark = check === 'ok' ? '✓' : check === 'mismatch' ? '✗' : '—'
  const meaning = check === 'ok' ? 'совпадает' : check === 'mismatch' ? 'не совпадает' : 'не проверялся'
  const tone =
    check === 'ok' ? 'text-status-committed' : check === 'mismatch' ? 'text-status-failed' : 'text-muted-foreground'
  return (
    <span aria-label={`${label}: ${meaning}`} className={`font-semibold ${tone}`}>
      {mark}
    </span>
  )
}

export function AuditStages({ stages }: { stages: AuditStage[] }) {
  return (
    <div className="overflow-x-auto rounded-xl border border-border bg-card shadow-soft">
      <table className="w-full min-w-[880px] text-left text-sm">
        <thead>
          <tr className="text-xs tracking-wide text-muted-foreground uppercase">
            <th scope="col" className="px-4 py-3 font-medium">Этап</th>
            <th scope="col" className="px-4 py-3 font-medium">Статус</th>
            <th scope="col" className="px-4 py-3 font-medium">Исполнитель</th>
            <th scope="col" className="px-4 py-3 font-medium">Организация</th>
            <th scope="col" className="px-4 py-3 font-medium">Время блока</th>
            <th scope="col" className="px-4 py-3 font-medium">Хеш деталей</th>
            <th scope="col" className="px-4 py-3 font-medium">Подписант</th>
            <th scope="col" className="px-4 py-3 font-medium">Товары</th>
            <th scope="col" className="px-4 py-3 font-medium">Подпись</th>
          </tr>
        </thead>
        <tbody>
          {stages.map((stage) => (
            <Fragment key={stage.actionId}>
              <tr className="border-t border-border align-top">
                <td className="px-4 py-3">
                  <div className="font-medium text-foreground">{stageName(stage.actionType)}</div>
                  {stage.txHash ? (
                    <div className="font-mono text-xs text-muted-foreground">
                      {truncateHash(stage.txHash, 8, 6)}
                    </div>
                  ) : null}
                </td>
                <td className="px-4 py-3">{statusText(stage)}</td>
                <td className="px-4 py-3">{stage.operator ?? '—'}</td>
                <td className="px-4 py-3">{stage.orgName ?? stage.orgId ?? '—'}</td>
                <td className="px-4 py-3">
                  {stage.blockTime ? formatDateTime(stage.blockTime.toISOString()) : '—'}
                </td>
                {CHECK_KEYS.map(({ key, label }) => (
                  <td key={key} className="px-4 py-3">
                    <CheckMark check={stage[key]} label={label} />
                  </td>
                ))}
              </tr>
              {stage.problems.length > 0 ? (
                <tr>
                  <td colSpan={9} className="px-4 pb-3">
                    <ul className="flex flex-col gap-1 text-xs text-status-failed">
                      {stage.problems.map((problem) => (
                        <li key={problem}>{problem}</li>
                      ))}
                    </ul>
                  </td>
                </tr>
              ) : null}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  )
}
