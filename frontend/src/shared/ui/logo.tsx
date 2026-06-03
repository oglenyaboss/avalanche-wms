import { cn } from '../lib'

type LogoSize = 'sm' | 'md' | 'lg'
type LogoTone = 'default' | 'invert'

const markSizeClassName: Record<LogoSize, string> = {
  sm: 'size-8 rounded-[0.5rem]',
  md: 'size-9 rounded-[0.55rem]',
  lg: 'size-12 rounded-xl',
}

const glyphSizeClassName: Record<LogoSize, string> = {
  sm: 'size-[1.15rem]',
  md: 'size-5',
  lg: 'size-7',
}

const wordSizeClassName: Record<LogoSize, string> = {
  sm: 'text-base',
  md: 'text-lg',
  lg: 'text-2xl',
}

const toneClassName: Record<
  LogoTone,
  { mark: string; word: string; subtitle: string }
> = {
  default: {
    mark: 'bg-primary text-primary-foreground ring-foreground/10',
    word: 'text-foreground',
    subtitle: 'text-muted-foreground',
  },
  invert: {
    mark: 'bg-primary-foreground text-primary ring-white/15',
    word: 'text-primary-foreground',
    subtitle: 'text-primary-foreground/70',
  },
}

// Isometric cube: reads as a storage box (warehouse) and as a block (chain).
// Three faces at descending opacity give depth while staying monochrome, so the
// mark works on the light sidebar, on dark surfaces, and as a favicon.
function CubeGlyph({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      className={className}
      aria-hidden="true"
      focusable="false"
    >
      <path d="M12 2.6 20.5 7.4 12 12.2 3.5 7.4Z" fill="currentColor" />
      <path
        d="M3.5 7.4 12 12.2v9.2L3.5 16.6Z"
        fill="currentColor"
        fillOpacity="0.74"
      />
      <path
        d="M20.5 7.4 12 12.2v9.2l8.5-4.8Z"
        fill="currentColor"
        fillOpacity="0.46"
      />
    </svg>
  )
}

type LogoProps = {
  className?: string
  size?: LogoSize
  tone?: LogoTone
  /** Render only the cube mark (collapsed sidebar, favicon-like spots). */
  markOnly?: boolean
  /** Optional tagline under the wordmark (login / brand panel). */
  subtitle?: string
}

export function Logo({
  className,
  size = 'md',
  tone = 'default',
  markOnly = false,
  subtitle,
}: LogoProps) {
  const t = toneClassName[tone]

  return (
    <span
      className={cn('inline-flex select-none items-center gap-2.5', className)}
    >
      <span
        className={cn(
          'grid shrink-0 place-items-center shadow-sm ring-1',
          markSizeClassName[size],
          t.mark
        )}
      >
        <CubeGlyph className={glyphSizeClassName[size]} />
      </span>
      {!markOnly && (
        <span className="flex min-w-0 flex-col leading-none">
          <span
            className={cn(
              'font-heading font-semibold tracking-tight',
              wordSizeClassName[size],
              t.word
            )}
          >
            WMS
          </span>
          {subtitle ? (
            <span
              className={cn('mt-1 truncate text-[0.7rem] font-medium tracking-tight', t.subtitle)}
            >
              {subtitle}
            </span>
          ) : (
            <span
              className={cn(
                'mt-0.5 text-[0.6rem] font-medium uppercase tracking-[0.2em]',
                t.subtitle
              )}
            >
              Ledger
            </span>
          )}
        </span>
      )}
    </span>
  )
}
