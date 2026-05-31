import { TableReceiving } from '@/features/receiving-table'
import { PageTitle } from '@/shared/ui'

export function ReceivingTablePage() {
  return (
    <main className="min-h-[calc(100vh-5rem)] px-6 py-10">
      <PageTitle className="mb-10">Приемка на столах</PageTitle>
      <TableReceiving />
    </main>
  )
}
