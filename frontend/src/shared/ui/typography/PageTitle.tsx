import type { ReactNode } from 'react'

type PageTitleProps = {
  children: ReactNode
  className?: string
}

export function PageTitle({ children, className }: PageTitleProps) {
  const titleClassName = [
    'text-center text-[3.5rem] leading-none font-bold tracking-tight',
    className,
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <h1 className={titleClassName}>{children}</h1>
  )
}
