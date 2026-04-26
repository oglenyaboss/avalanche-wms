import { routes } from '@/shared/config/routes'
import { Button } from '@/shared/ui'

export function NotFoundPage() {
  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-4 px-6 text-center">
      <p className="text-sm text-muted-foreground">404</p>
      <h1 className="text-3xl font-semibold">Page not found</h1>
      <p className="max-w-md text-sm text-muted-foreground">
        The requested route does not exist in the current application setup.
      </p>
      <Button asChild>
        <a href={routes.home}>Back to home</a>
      </Button>
    </main>
  )
}
