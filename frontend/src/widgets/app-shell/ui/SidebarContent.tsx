import { LogoutButton } from '@/features/auth'
import { Logo } from '@/shared/ui'

import { SidebarNav } from './SidebarNav'

type SidebarContentProps = {
  onNavigate?: () => void
}

export function SidebarContent({ onNavigate }: SidebarContentProps) {
  return (
    <div className="flex h-full flex-col">
      <div className="px-5 py-5">
        <Logo size="md" />
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-1">
        <SidebarNav onNavigate={onNavigate} />
      </div>

      <div className="border-t border-border p-3">
        <div className="mb-1.5 flex items-center gap-3 rounded-lg px-2 py-1.5">
          <span className="grid size-9 shrink-0 place-items-center rounded-full bg-secondary text-sm font-semibold text-foreground">
            О
          </span>
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-foreground">
              Оператор
            </p>
            <p className="truncate text-xs text-muted-foreground">
              Смена активна
            </p>
          </div>
        </div>
        <LogoutButton
          variant="ghost"
          withIcon
          className="w-full justify-start text-muted-foreground"
        />
      </div>
    </div>
  )
}
