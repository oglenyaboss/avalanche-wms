import { Navigate, Outlet } from 'react-router'

import { useSessionStore } from '@/entities/session'
import { routes } from '@/shared/config'

export function ProtectedRoute() {
  const isAuthenticated = useSessionStore((state) => state.isAuthenticated)

  if (!isAuthenticated) {
    return <Navigate to={routes.login} replace />
  }

  return <Outlet />
}
