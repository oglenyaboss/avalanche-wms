// On-chain event domain primitives, shared across the analytics dashboard and
// product traceability. The four states mirror the backend onchain_event_status
// enum, lowercased for use as map keys / CSS class suffixes.

export type ChainStatus = 'committed' | 'sent' | 'pending' | 'failed'

export const CHAIN_STATUS_ORDER: ChainStatus[] = [
  'committed',
  'sent',
  'pending',
  'failed',
]

// Semantic accent — used ONLY for on-chain event state. These resolve to the
// --status-* CSS custom properties (light/dark aware) defined in app/index.css.
export const STATUS_COLOR: Record<ChainStatus, string> = {
  committed: 'var(--status-committed)',
  sent: 'var(--status-sent)',
  pending: 'var(--status-pending)',
  failed: 'var(--status-failed)',
}

const CHAIN_STATUS_LABELS: Record<ChainStatus, string> = {
  committed: 'Подтверждено',
  sent: 'Отправлено',
  pending: 'В ожидании',
  failed: 'Ошибка',
}

export function chainStatusLabel(status: ChainStatus): string {
  return CHAIN_STATUS_LABELS[status]
}

// Russian labels for the four FSM warehouse stages (aggregate_type values).
const STAGE_LABELS: Record<string, string> = {
  receiving: 'Приёмка',
  putaway: 'Раскладка',
  picking: 'Сборка',
  shipping: 'Отгрузка',
}

export function stageLabel(aggregateType: string): string {
  return STAGE_LABELS[aggregateType] ?? aggregateType
}
