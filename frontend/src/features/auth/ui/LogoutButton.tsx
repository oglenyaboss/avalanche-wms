import { Logout01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import type { ComponentProps } from 'react'

import { Button } from '@/shared/ui'

import { useLogout } from '../model/useLogout'

type LogoutButtonProps = {
  className?: string
  variant?: ComponentProps<typeof Button>['variant']
  withIcon?: boolean
}

export function LogoutButton({
  className,
  variant = 'outline',
  withIcon = false,
}: LogoutButtonProps) {
  const logout = useLogout()

  return (
    <Button variant={variant} onClick={logout} className={className}>
      {withIcon ? (
        <HugeiconsIcon icon={Logout01Icon} strokeWidth={1.8} />
      ) : null}
      Выйти
    </Button>
  )
}
