import type { ChainStatus } from './types'

// Russian-localized formatting helpers for the analytics dashboard. Kept pure
// and side-effect free so they can be unit-tested without a DOM.

const numberFmt = new Intl.NumberFormat('ru-RU')

/** Thousands-grouped integer, e.g. 1234 → "1 234". */
export function formatNumber(value: number): string {
  return numberFmt.format(value)
}

/**
 * A 0..1 rate as a percentage with one decimal, e.g. 0.8372 → "83,7%".
 * Values outside the range are clamped so a bad payload never renders ">100%".
 */
export function formatPercent(rate: number): string {
  const clamped = Math.min(1, Math.max(0, rate))
  return `${(clamped * 100).toLocaleString('ru-RU', {
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  })}%`
}

/** Whole-percent share of part within total (0 when total is 0). */
export function sharePercent(part: number, total: number): number {
  if (total <= 0) return 0
  return (part / total) * 100
}

/** ISO timestamp → "2 июн, 07:01" (Moscow-agnostic, local). Falls back to the
 *  raw string if it cannot be parsed. */
export function formatDateTime(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return date.toLocaleString('ru-RU', {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** "YYYY-MM-DD" axis label → short "31.05". Falls back to the raw value. */
export function formatDayShort(day: string): string {
  const parts = day.split('-')
  if (parts.length !== 3) return day
  return `${parts[2]}.${parts[1]}`
}

/** Middle-truncate a long hex hash for compact display: 0xabcd…1e76. */
export function truncateHash(hash: string | null, lead = 6, tail = 4): string {
  if (!hash) return '—'
  if (hash.length <= lead + tail + 1) return hash
  return `${hash.slice(0, lead)}…${hash.slice(-tail)}`
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

// Russian labels for product / order / dispatch lifecycle statuses. Unknown
// values pass through unchanged so a new enum value is still rendered.
const LIFECYCLE_LABELS: Record<string, string> = {
  // product_status
  RECEIVED: 'Принято',
  STORED: 'На хранении',
  ALLOCATED: 'Зарезервировано',
  ASSEMBLED: 'Собрано',
  READY_TO_SHIP: 'К отгрузке',
  SHIPPED: 'Отгружено',
  // order_status
  NEW: 'Новый',
  PARTIALLY_SHIPPED: 'Частично отгружен',
  // outbound_dispatch_status
  SCHEDULED: 'Запланирован',
  AT_GATE: 'На воротах',
  DEPARTED: 'Отбыл',
  CANCELLED: 'Отменён',
}

export function lifecycleLabel(status: string): string {
  return LIFECYCLE_LABELS[status] ?? status
}
