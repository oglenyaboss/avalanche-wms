import { cn } from '@/shared/lib'

interface SkeletonProps {
  className?: string
}

// Minimal loading placeholder. Feature-local (the shared UI kit has no Skeleton
// yet) so the analytics MR stays self-contained.
export function Skeleton({ className }: SkeletonProps) {
  return (
    <div
      className={cn('animate-pulse rounded-md bg-muted', className)}
      aria-hidden="true"
    />
  )
}
