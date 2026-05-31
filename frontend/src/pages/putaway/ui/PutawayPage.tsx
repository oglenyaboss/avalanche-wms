import { Putaway } from '@/features/putaway'
import { PageTitle } from '@/shared/ui'

export function PutawayPage() {
  return (
    <main className="min-h-[calc(100vh-5rem)] px-6 py-10">
      <PageTitle className="mb-10">Раскладка</PageTitle>
      <Putaway />
    </main>
  )
}
