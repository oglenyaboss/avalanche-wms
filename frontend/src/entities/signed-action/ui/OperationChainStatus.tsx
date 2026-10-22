import { Alert, AlertDescription } from '@/shared/ui'

import { operationStatusText } from '../model/status'
import { useOperationStatus } from '../model/useOperationStatus'

interface OperationChainStatusProps {
  operationId: string
}

// Live chain verdict for a signed operation: polls while parts are in
// flight, then shows the final applied / rejected / failed text.
export function OperationChainStatus({ operationId }: OperationChainStatusProps) {
  const { data: status } = useOperationStatus(operationId)

  if (!status) {
    return null
  }

  const applied =
    status.parts.length > 0 && status.parts.every((part) => part.state === 'APPLIED')
  const failed = status.parts.some(
    (part) => part.state === 'REJECTED' || part.state === 'FAILED',
  )

  return (
    <Alert
      role="status"
      variant={failed ? 'destructive' : applied ? 'success' : 'default'}
    >
      <AlertDescription className="col-span-2">
        {operationStatusText(status)}
      </AlertDescription>
    </Alert>
  )
}
