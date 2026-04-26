import { logoShadowSvg } from '@/shared/assets'

import { cn } from '../lib'

type LogoSize = 'sm' | 'lg'

const logoSizeClassName: Record<LogoSize, string> = {
  sm: 'h-10 sm:h-11',
  lg: 'h-auto w-full max-w-[min(37rem,100%)]',
}

type LogoProps = {
  className?: string
  size?: LogoSize
}

export function Logo({ className, size = 'sm' }: LogoProps) {
  return (
    <img
      src={logoShadowSvg}
      alt="WMS"
      draggable={false}
      className={cn(
        'block select-none object-contain',
        logoSizeClassName[size],
        className
      )}
    />
  )
}
