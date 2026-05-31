import type { CargoplaceStatus } from './types'

type CargoplaceStatusTone = 'pending' | 'received' | 'missing' | 'neutral'

interface CargoplaceStatusMeta {
  label: string
  tone: CargoplaceStatusTone
}

const cargoplaceStatusMeta: Record<CargoplaceStatus, CargoplaceStatusMeta> = {
  EXPECTED: { label: 'Ожидается', tone: 'pending' },
  RECEIVED_AT_GATE: { label: 'Принято', tone: 'received' },
  NOT_RECEIVED: { label: 'Не принято', tone: 'missing' },
  TABLE_IN_PROGRESS: { label: 'На столе', tone: 'neutral' },
  TABLE_CLOSED: { label: 'Обработано', tone: 'neutral' },
}

// Tolerate a status outside the known set (e.g. a server-side addition the
// frontend hasn't caught up to) instead of crashing the list render.
export function getCargoplaceStatusLabel(status: CargoplaceStatus): string {
  return cargoplaceStatusMeta[status]?.label ?? status
}

export function getCargoplaceStatusTone(
  status: CargoplaceStatus,
): CargoplaceStatusTone {
  return cargoplaceStatusMeta[status]?.tone ?? 'neutral'
}
