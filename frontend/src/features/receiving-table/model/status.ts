import { rejectReasonText } from '@/shared/lib'

import type { OperationStatus } from './types'

// Operator-facing verdict of a signed close operation: whether the chain
// applied the parts, rejected them, or is still recording them.
export function operationStatusText(status: OperationStatus): string {
  const parts = status.parts

  if (parts.length > 0 && parts.every((part) => part.state === 'APPLIED')) {
    return 'Записано в цепь'
  }

  const rejected = parts.find((part) => part.state === 'REJECTED')
  if (rejected) {
    return `Отклонено цепью: ${rejectReasonText(rejected.rejectReason)}`
  }

  if (parts.some((part) => part.state === 'FAILED')) {
    return 'Не удалось записать в цепь'
  }

  return 'Отправлено в цепь, ждём подтверждения'
}
