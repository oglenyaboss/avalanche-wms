import type { ReactNode } from 'react'

type AuthFormShellProps = {
  children: ReactNode
  status?: ReactNode
  title: string
  description?: string
}

export function AuthFormShell({
  children,
  status,
  title,
  description,
}: AuthFormShellProps) {
  return (
    <div className="w-full max-w-[360px]">
      <header className="mb-7">
        <h1 className="text-3xl font-semibold tracking-tight text-foreground">
          {title}
        </h1>
        {description ? (
          <p className="mt-2 text-sm text-muted-foreground">{description}</p>
        ) : null}
      </header>
      {children}
      {status ? <div className="mt-5">{status}</div> : null}
    </div>
  )
}
