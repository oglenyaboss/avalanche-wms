import { ViewIcon, ViewOffSlashIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState } from 'react'

import { cn } from '../lib'
import { Button } from './button'
import { Input } from './input'

function PasswordInput({
  className,
  ...props
}: Omit<React.ComponentProps<typeof Input>, 'type'>) {
  const [isVisible, setIsVisible] = useState(false)

  return (
    <div className="relative">
      <Input
        type={isVisible ? 'text' : 'password'}
        className={cn('pr-12', className)}
        {...props}
      />
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        className="absolute top-1/2 right-1.5 h-8 w-8 -translate-y-1/2 rounded-full text-muted-foreground hover:bg-transparent hover:text-foreground focus-visible:border-transparent focus-visible:ring-0"
        onClick={() => setIsVisible((current) => !current)}
        aria-label={isVisible ? 'Скрыть пароль' : 'Показать пароль'}
      >
        <HugeiconsIcon
          icon={isVisible ? ViewOffSlashIcon : ViewIcon}
          strokeWidth={1.7}
        />
      </Button>
    </div>
  )
}

export { PasswordInput }
