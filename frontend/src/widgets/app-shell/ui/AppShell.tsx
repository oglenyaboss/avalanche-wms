import { Outlet } from 'react-router'

import { AppHeader } from './AppHeader'

export function AppShell() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <AppHeader />
      <Outlet />
    </div>
  )
}
