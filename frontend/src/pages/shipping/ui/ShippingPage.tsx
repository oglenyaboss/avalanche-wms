import { Shipping } from '@/features/shipping'
import { PageTitle } from '@/shared/ui'

export function ShippingPage() {
  return (
    <main className="min-h-[calc(100vh-5rem)] px-6 py-10">
      <PageTitle className="mb-10">Отгрузка</PageTitle>
      <Shipping />
    </main>
  )
}
