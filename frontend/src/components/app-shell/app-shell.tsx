"use client";

import { Logout01Icon, Menu01Icon, WarehouseIcon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState, type ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { ScannerEmulator } from "@/components/scan/scanner-emulator";
import { roleLabels } from "@/lib/auth/roles";
import { clearSession, type Session } from "@/lib/auth/session";
import { mocksEnabled } from "@/lib/env";
import { ScanProvider } from "@/lib/scan/scan-context";

import { NavMenu } from "./nav-menu";
import { navItemFor } from "./navigation";

export function AppShell({ session, children }: { session: Session; children: ReactNode }) {
  const pathname = usePathname();
  const [menuOpen, setMenuOpen] = useState(false);
  const item = navItemFor(pathname);
  const allowed = !item || item.roles.includes(session.user.role);

  return (
    <div className="flex min-h-svh">
      <aside className="hidden w-64 shrink-0 flex-col gap-6 border-r bg-sidebar p-4 text-sidebar-foreground lg:flex">
        <Brand />
        <NavMenu role={session.user.role} />
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <ScanProvider>
          <header className="flex h-14 items-center gap-2 border-b px-4">
            <Sheet open={menuOpen} onOpenChange={setMenuOpen}>
              <SheetTrigger asChild>
                <Button variant="ghost" size="icon-lg" className="lg:hidden" aria-label="Открыть меню">
                  <HugeiconsIcon icon={Menu01Icon} className="size-6" />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="w-72 gap-6 bg-sidebar p-4 text-sidebar-foreground">
                <SheetHeader className="p-0">
                  <SheetTitle asChild>
                    <div>
                      <Brand />
                    </div>
                  </SheetTitle>
                </SheetHeader>
                <NavMenu role={session.user.role} onNavigate={() => setMenuOpen(false)} />
              </SheetContent>
            </Sheet>

            <div className="ml-auto flex items-center gap-3">
              {mocksEnabled && <ScannerEmulator />}
              <span className="text-sm font-medium" data-testid="current-user">
                {session.user.username}
              </span>
              <Badge variant="secondary">{roleLabels[session.user.role]}</Badge>
              <Button variant="ghost" size="icon-lg" aria-label="Выйти" onClick={clearSession}>
                <HugeiconsIcon icon={Logout01Icon} className="size-5" />
              </Button>
            </div>
          </header>

          <main className="flex flex-1 flex-col gap-6 p-4 lg:p-6">
            {allowed ? children : <Forbidden />}
          </main>
        </ScanProvider>
      </div>
    </div>
  );
}

function Brand() {
  return (
    <Link href="/" className="flex items-center gap-2 px-3 text-base font-semibold">
      <HugeiconsIcon icon={WarehouseIcon} className="size-6" />
      Терминал склада
    </Link>
  );
}

function Forbidden() {
  return (
    <div className="flex flex-col gap-2">
      <h1 className="text-2xl font-semibold">Нет доступа</h1>
      <p className="text-muted-foreground">Этот раздел недоступен для вашей роли.</p>
    </div>
  );
}
