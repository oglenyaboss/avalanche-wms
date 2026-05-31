import {
  AlertCircleIcon,
  CheckmarkCircle02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import type { ReactNode } from 'react'

import { Alert, AlertDescription, AlertTitle } from '@/shared/ui'

type AuthStatusAlertProps = {
  description: ReactNode
  title: string
  variant: 'destructive' | 'success'
}

export function AuthStatusAlert({
  description,
  title,
  variant,
}: AuthStatusAlertProps) {
  return (
    <Alert variant={variant}>
      <HugeiconsIcon
        icon={variant === 'success' ? CheckmarkCircle02Icon : AlertCircleIcon}
        strokeWidth={1.7}
        className="mt-0.5 size-5"
      />
      <div className="space-y-1">
        <AlertTitle>{title}</AlertTitle>
        <AlertDescription>{description}</AlertDescription>
      </div>
    </Alert>
  )
}
