import * as React from 'react'

import { cn } from '../lib'

function Input({ className, type, ...props }: React.ComponentProps<'input'>) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        'h-11 w-full min-w-0 rounded-md border border-input bg-background px-4 py-2 text-base transition-[border-color,box-shadow] outline-none file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-placeholder hover:border-ring hover:ring-[3px] hover:ring-ring/12 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/18 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive hover:aria-invalid:border-destructive hover:aria-invalid:ring-destructive/12 focus-visible:aria-invalid:border-destructive focus-visible:aria-invalid:ring-destructive/18',
        className,
      )}
      {...props}
    />
  )
}

export { Input }
