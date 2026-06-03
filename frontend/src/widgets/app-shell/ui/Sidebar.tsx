import { SidebarContent } from './SidebarContent'

export function Sidebar() {
  return (
    <aside className="fixed inset-y-0 left-0 z-40 hidden w-64 border-r border-border bg-sidebar lg:block">
      <SidebarContent />
    </aside>
  )
}
