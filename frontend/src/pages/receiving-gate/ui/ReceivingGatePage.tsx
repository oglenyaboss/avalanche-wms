import { GateReceiving } from '@/features/receiving-gate'
import { PageTitle } from '@/shared/ui'

export function ReceivingGatePage() {
  return (
    <main className="min-h-[calc(100vh-5rem)] px-6 py-10">
      <PageTitle className="mb-10">Приемка на воротах</PageTitle>
      <GateReceiving />
    </main>
  )
}
