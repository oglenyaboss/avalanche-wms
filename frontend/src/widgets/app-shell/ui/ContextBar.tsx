import { Menu01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState } from 'react'
import { useLocation } from 'react-router'

import {
  Button,
  Sheet,
  SheetContent,
  SheetTitle,
  SheetTrigger,
} from '@/shared/ui'

import { navigationItems } from '../config/navigationItems'
import { SidebarContent } from './SidebarContent'

function usePageTitle() {
  const { pathname } = useLocation()
  const exact = navigationItems.find((item) => item.to === pathname)
  if (exact) return exact.label
  const prefixed = navigationItems.find(
    (item) => item.to !== '/' && pathname.startsWith(item.to)
  )
  return prefixed?.label ?? 'WMS'
}

export function ContextBar() {
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const title = usePageTitle()

  return (
    <header className="sticky top-0 z-30 flex h-16 items-center gap-3 border-b border-border bg-background/80 px-4 backdrop-blur supports-backdrop-filter:bg-background/70 lg:px-8">
      <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
        <SheetTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            className="lg:hidden"
            aria-label="Открыть меню"
          >
            <HugeiconsIcon icon={Menu01Icon} strokeWidth={1.8} />
          </Button>
        </SheetTrigger>
        <SheetContent side="left" className="w-72 p-0" showCloseButton={false}>
          <SheetTitle className="sr-only">Навигация</SheetTitle>
          <SidebarContent onNavigate={() => setMobileNavOpen(false)} />
        </SheetContent>
      </Sheet>

      <h1 className="truncate text-lg font-semibold tracking-tight text-foreground">
        {title}
      </h1>
    </header>
  )
}
