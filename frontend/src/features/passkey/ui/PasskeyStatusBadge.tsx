import { cn } from '@/shared/lib'

import { passkeyStatusLabel } from '../model/status'
import type { PasskeyStatus } from '../model/types'

const STATUS_TONE: Record<PasskeyStatus, string> = {
  ACTIVE: 'bg-status-committed/12 text-status-committed',
  PENDING: 'bg-status-pending/12 text-status-pending',
  REJECTED: 'bg-status-failed/12 text-status-failed',
  REVOKED: 'bg-muted text-muted-foreground',
}

interface PasskeyStatusBadgeProps {
  status: PasskeyStatus
}

export function PasskeyStatusBadge({ status }: PasskeyStatusBadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium',
        STATUS_TONE[status]
      )}
    >
      {passkeyStatusLabel(status)}
    </span>
  )
}
