import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'

import { cn } from '../lib'

const alertVariants = cva(
  'grid w-full grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-xl border bg-card px-4 py-4 text-card-foreground',
  {
    variants: {
      variant: {
        default: 'border-border',
        destructive: 'border-destructive/30 text-destructive',
        success: 'border-border text-foreground',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  },
)

function Alert({
  className,
  variant,
  ...props
}: React.ComponentProps<'div'> & VariantProps<typeof alertVariants>) {
  return (
    <div
      role="alert"
      data-slot="alert"
      data-variant={variant}
      className={cn(alertVariants({ variant }), className)}
      {...props}
    />
  )
}

function AlertTitle({ className, ...props }: React.ComponentProps<'h5'>) {
  return (
    <h5
      data-slot="alert-title"
      className={cn('text-base font-semibold leading-6', className)}
      {...props}
    />
  )
}

function AlertDescription({
  className,
  ...props
}: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="alert-description"
      className={cn('text-sm leading-6 [&_p]:leading-6', className)}
      {...props}
    />
  )
}

export { Alert, AlertDescription, AlertTitle }
