import { BarCode01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon, type IconSvgElement } from '@hugeicons/react'
import type { ReactNode } from 'react'

import { cn } from '@/shared/lib'

type ScanSceneProps = {
  /** Optional small kicker above the title. */
  eyebrow?: string
  /** Stage name — the primary heading for the operation. */
  title: string
  /** One-line orientation for the operator. */
  description?: string
  /** Glyph for the console header; defaults to a barcode. */
  icon?: IconSvgElement
  /** Banner (success / empty-state alert) pinned above the console. */
  banner?: ReactNode
  /** Console body — the scan form or selection list. Logic stays in the child. */
  children: ReactNode
  className?: string
}

// A shared layout shell for the entry screen of every warehouse operation.
// Purely presentational: it frames the scan affordance with depth so the screen
// reads as a focused scan station. It never owns focus, form, or submit logic.
export function ScanScene({
  eyebrow,
  title,
  description,
  icon = BarCode01Icon,
  banner,
  children,
  className,
}: ScanSceneProps) {
  return (
    <div className={cn('mx-auto w-full max-w-3xl', className)}>
      <header className="mb-6 flex flex-col gap-2.5">
        {eyebrow ? (
          <span className="inline-flex items-center gap-2 text-xs font-medium tracking-[0.14em] text-muted-foreground uppercase">
            <span className="size-1.5 rounded-full bg-status-committed" />
            {eyebrow}
          </span>
        ) : null}
        {/* h2, not h1: the app-shell ContextBar already renders the page-level
            h1, so this stays one level below it (avoids a duplicate h1). */}
        <h2 className="text-3xl font-semibold tracking-tight text-balance sm:text-4xl">
          {title}
        </h2>
        {description ? (
          <p className="max-w-prose leading-relaxed text-muted-foreground">
            {description}
          </p>
        ) : null}
      </header>

      {banner ? <div className="mb-5">{banner}</div> : null}

      {/* Console — the scan affordance, lifted with depth + faint atmosphere. */}
      <section className="relative overflow-hidden rounded-2xl bg-card p-6 shadow-card ring-1 ring-foreground/10 sm:p-8">
        <div
          aria-hidden
          className="bg-dotgrid pointer-events-none absolute inset-0 opacity-70"
        />
        <div
          aria-hidden
          className="pointer-events-none absolute -top-16 -right-16 size-48 rounded-full bg-status-committed/[0.06] blur-3xl"
        />
        <div className="relative flex flex-col gap-5">
          <span className="grid size-11 place-items-center rounded-xl bg-secondary text-foreground ring-1 ring-foreground/5">
            <HugeiconsIcon
              icon={icon}
              strokeWidth={1.8}
              className="size-5"
              aria-hidden
            />
          </span>
          {children}
        </div>
      </section>
    </div>
  )
}
