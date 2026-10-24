"use client";

import { HugeiconsIcon } from "@hugeicons/react";
import Link from "next/link";
import { usePathname } from "next/navigation";

import type { Role } from "@/lib/auth/roles";
import { cn } from "@/lib/utils";

import { navigationFor } from "./navigation";

export function NavMenu({ role, onNavigate }: { role: Role; onNavigate?: () => void }) {
  const pathname = usePathname();
  const sections = navigationFor(role);

  if (sections.length === 0) {
    return <p className="px-3 text-sm text-muted-foreground">Для вашей роли нет доступных разделов.</p>;
  }

  return (
    <nav aria-label="Разделы" className="flex flex-col gap-5">
      {sections.map((section) => (
        <div key={section.title} className="flex flex-col gap-1">
          <p className="px-3 text-xs font-medium tracking-wide text-muted-foreground uppercase">{section.title}</p>
          {section.items.map((item) => {
            const active = pathname === item.href || pathname.startsWith(`${item.href}/`);
            return (
              <Link
                key={item.href}
                href={item.href}
                onClick={onNavigate}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex min-h-11 items-center gap-3 rounded-xl px-3 text-sm font-medium transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                  active && "bg-sidebar-accent text-sidebar-accent-foreground",
                )}
              >
                <HugeiconsIcon icon={item.icon} className="size-5" />
                {item.title}
              </Link>
            );
          })}
        </div>
      ))}
    </nav>
  );
}
