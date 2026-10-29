"use client";

import { HugeiconsIcon } from "@hugeicons/react";
import Link from "next/link";

import { navigationFor } from "@/components/app-shell/navigation";
import { roleLabels } from "@/lib/auth/roles";
import { useSession } from "@/lib/auth/session";

export default function HomePage() {
  const session = useSession();
  if (!session) return null;
  const sections = navigationFor(session.user.role);

  return (
    <>
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold">Здравствуйте, {session.user.username}</h1>
        <p className="text-muted-foreground">{roleLabels[session.user.role]}</p>
      </div>
      {sections.length === 0 && <p className="text-muted-foreground">Для вашей роли нет доступных разделов.</p>}
      {sections.map((section) => (
        <section key={section.title} className="flex flex-col gap-3">
          <h2 className="text-sm font-medium tracking-wide text-muted-foreground uppercase">{section.title}</h2>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {section.items.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className="flex min-h-16 items-center gap-3 rounded-2xl border bg-card p-4 font-medium transition-colors hover:bg-accent"
              >
                <HugeiconsIcon icon={item.icon} className="size-6" />
                {item.title}
              </Link>
            ))}
          </div>
        </section>
      ))}
    </>
  );
}
