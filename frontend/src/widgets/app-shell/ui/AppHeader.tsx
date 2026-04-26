import { NavLink } from 'react-router'

import { LogoutButton } from '@/features/auth'
import {
  Button,
  Logo,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/shared/ui'

import { navigationItems } from '../config/navigationItems'

export function AppHeader() {
  return (
    <header className="sticky top-0 z-40 flex min-h-20 flex-col items-start gap-3 bg-background/95 px-4 py-4 backdrop-blur supports-backdrop-filter:bg-background/80 sm:flex-row sm:items-center sm:gap-8 sm:px-8">
      <Logo />
      <nav className="flex w-full flex-1 items-center gap-2 overflow-x-auto sm:justify-center sm:gap-3">
        {navigationItems.map((item) => (
          <Tooltip key={item.label}>
            <TooltipTrigger asChild>
              {item.to ? (
                <Button variant="ghost" size="sm" asChild>
                  <NavLink to={item.to}>{item.label}</NavLink>
                </Button>
              ) : (
                <Button variant="ghost" size="sm" type="button">
                  {item.label}
                </Button>
              )}
            </TooltipTrigger>
            <TooltipContent>{item.title}</TooltipContent>
          </Tooltip>
        ))}
      </nav>
      <LogoutButton />
    </header>
  )
}
