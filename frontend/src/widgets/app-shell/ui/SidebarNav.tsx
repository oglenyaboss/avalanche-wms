import { HugeiconsIcon } from '@hugeicons/react'
import { NavLink } from 'react-router'

import { cn } from '@/shared/lib'

import { navigationItems } from '../config/navigationItems'

type SidebarNavProps = {
  onNavigate?: () => void
}

export function SidebarNav({ onNavigate }: SidebarNavProps) {
  return (
    <nav className="flex flex-col gap-0.5">
      {navigationItems.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.to === '/'}
          onClick={onNavigate}
          title={item.title}
          className={({ isActive }) =>
            cn(
              'flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors',
              isActive
                ? 'bg-secondary font-medium text-foreground'
                : 'text-muted-foreground hover:bg-secondary/60 hover:text-foreground'
            )
          }
        >
          <HugeiconsIcon
            icon={item.icon}
            strokeWidth={1.8}
            className="size-[1.15rem] shrink-0"
          />
          <span className="truncate">{item.label}</span>
        </NavLink>
      ))}
    </nav>
  )
}
