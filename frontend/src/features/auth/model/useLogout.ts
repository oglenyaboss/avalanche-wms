import { useNavigate } from 'react-router'

import { useSessionStore } from '@/entities/session'
import { routes } from '@/shared/config'

export function useLogout() {
  const navigate = useNavigate()
  const logout = useSessionStore((state) => state.logout)

  return () => {
    logout()
    navigate(routes.login, { replace: true })
  }
}
