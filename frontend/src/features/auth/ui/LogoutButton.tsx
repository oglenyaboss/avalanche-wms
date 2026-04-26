import { Button } from '@/shared/ui'

import { useLogout } from '../model/useLogout'

export function LogoutButton() {
  const logout = useLogout()

  return (
    <Button variant="outline" onClick={logout}>
      Выйти
    </Button>
  )
}
