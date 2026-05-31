import type { ReactNode } from 'react'

import { PageTitle } from '@/shared/ui'

type AuthFormShellProps = {
  children: ReactNode
  status?: ReactNode
  title: string
}

export function AuthFormShell({
  children,
  status,
  title,
}: AuthFormShellProps) {
  return (
    <section className="grid min-h-[calc(100vh-6rem)] w-full grid-rows-[1fr_auto_1fr]">
      <div className="flex items-end justify-center pb-8">
        <div className="w-[348px] max-w-full">
          <PageTitle>{title}</PageTitle>
        </div>
      </div>
      <div className="flex items-center justify-center">
        <div className="w-[348px] max-w-full min-h-[252px]">{children}</div>
      </div>
      <div className="flex items-start justify-center pt-8">
        <div className="min-h-[124px] w-[348px] max-w-full">{status}</div>
      </div>
    </section>
  )
}
