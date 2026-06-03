import { Outlet } from 'react-router'

import { ContextBar } from './ContextBar'
import { Sidebar } from './Sidebar'

export function AppShell() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Sidebar />
      <div className="flex min-h-screen flex-col lg:pl-64">
        <ContextBar />
        <main className="flex-1 px-4 py-6 lg:px-8 lg:py-8">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
