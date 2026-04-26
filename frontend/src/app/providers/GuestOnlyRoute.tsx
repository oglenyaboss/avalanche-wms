import { Navigate, Outlet } from 'react-router'

import { useSessionStore } from '@/entities/session'
import { routes } from '@/shared/config'

export function GuestOnlyRoute() {
  const isAuthenticated = useSessionStore((state) => state.isAuthenticated)

  if (isAuthenticated) {
    return <Navigate to={routes.home} replace />
  }

  return <Outlet />
}

