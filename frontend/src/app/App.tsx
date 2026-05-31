import { AppRouter } from '@/app/providers/AppRouter'
import { AuthProvider } from '@/app/providers/AuthProvider'
import { QueryClientProvider } from '@/app/providers/QueryClientProvider'
import { TooltipProvider } from '@/shared/ui'

import '@/app/index.css'

export function App() {
  return (
    <QueryClientProvider>
      <TooltipProvider>
        <AuthProvider>
          <AppRouter />
        </AuthProvider>
      </TooltipProvider>
    </QueryClientProvider>
  )
}
