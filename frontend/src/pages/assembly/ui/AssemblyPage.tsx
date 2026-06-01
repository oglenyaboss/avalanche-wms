import { Assembly } from '@/features/assembly'
import { PageTitle } from '@/shared/ui'

export function AssemblyPage() {
  return (
    <main className="min-h-[calc(100vh-5rem)] px-6 py-10">
      <PageTitle className="mb-10">Сборка</PageTitle>
      <Assembly />
    </main>
  )
}
