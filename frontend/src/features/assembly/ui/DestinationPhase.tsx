import type {
  AllocationResult,
  AssemblyTask,
  Destination,
} from '@/entities/assembly'
import {
  Alert,
  AlertDescription,
  Button,
  Card,
  CardContent,
} from '@/shared/ui'

import { ChevronRightIcon } from './icons'

interface DestinationPhaseProps {
  destinations: Destination[]
  selectedDestination: Destination | null
  allocation: AllocationResult | null
  tasks: AssemblyTask[]
  successMessage: string | null
  onSelect: (destination: Destination) => void
  onDeselect: () => void
  onAllocate: () => void
  onStartPicking: () => void
  isLoadingDestinations: boolean
  isAllocating: boolean
  isLoadingTasks: boolean
}

// The store picker, then the selected-store panel: allocate the store's orders,
// see how many pick tasks are waiting, and start the assembly.
function DestinationList({
  destinations,
  isLoading,
  onSelect,
}: {
  destinations: Destination[]
  isLoading: boolean
  onSelect: (destination: Destination) => void
}) {
  if (isLoading) {
    return (
      <p className="text-sm text-muted-foreground">Загрузка магазинов...</p>
    )
  }

  if (destinations.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        Магазины не найдены. Обратитесь к администратору.
      </p>
    )
  }

  return (
    <ul className="flex flex-col gap-3">
      {destinations.map((destination) => (
        <li key={destination.destinationId}>
          <Card size="sm" className="ring-foreground/10">
            <CardContent className="p-0">
              <button
                type="button"
                onClick={() => onSelect(destination)}
                className="flex w-full items-center justify-between gap-4 px-4 py-3 text-left transition-colors hover:bg-accent/50 focus-visible:bg-accent/50 focus-visible:outline-none"
              >
                <span className="flex flex-col gap-0.5">
                  <span className="font-medium">{destination.name}</span>
                  {destination.address ? (
                    <span className="text-sm text-muted-foreground">
                      {destination.address}
                    </span>
                  ) : null}
                </span>
                <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" />
              </button>
            </CardContent>
          </Card>
        </li>
      ))}
    </ul>
  )
}

export function DestinationPhase({
  destinations,
  selectedDestination,
  allocation,
  tasks,
  successMessage,
  onSelect,
  onDeselect,
  onAllocate,
  onStartPicking,
  isLoadingDestinations,
  isAllocating,
  isLoadingTasks,
}: DestinationPhaseProps) {
  // Store picker: no store chosen yet.
  if (!selectedDestination) {
    return (
      <section className="mx-auto flex max-w-xl flex-col gap-6">
        {successMessage ? (
          <Alert variant="success">
            <AlertDescription>{successMessage}</AlertDescription>
          </Alert>
        ) : null}
        <h2 className="text-lg font-medium">Выберите магазин для сборки</h2>
        <DestinationList
          destinations={destinations}
          isLoading={isLoadingDestinations}
          onSelect={onSelect}
        />
      </section>
    )
  }

  // Selected-store panel.
  const hasShortage =
    allocation !== null && allocation.insufficientOrders.length > 0

  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="self-start"
          onClick={onDeselect}
        >
          ← Выбрать другой магазин
        </Button>
        <h2 className="text-lg font-semibold">{selectedDestination.name}</h2>
        {selectedDestination.address ? (
          <p className="text-sm text-muted-foreground">
            {selectedDestination.address}
          </p>
        ) : null}
      </div>

      <div className="flex flex-col gap-3">
        <Button type="button" onClick={onAllocate} disabled={isAllocating}>
          {isAllocating ? 'Аллокация...' : 'Аллоцировать заказы'}
        </Button>

        {allocation ? (
          <Alert variant="success">
            <AlertDescription>
              Аллоцировано заказов: {allocation.allocatedOrders}, товаров:{' '}
              {allocation.allocatedProducts}.
            </AlertDescription>
          </Alert>
        ) : null}

        {hasShortage ? (
          <Alert variant="default">
            <AlertDescription className="col-span-2 flex flex-col gap-1">
              <span className="font-medium">
                Не хватило остатка по заказам:
              </span>
              <ul className="flex flex-col gap-1">
                {allocation.insufficientOrders.map((order) => (
                  <li key={order.orderId} className="text-sm">
                    Заказ {order.orderId}:{' '}
                    {order.missingSkus
                      .map((sku) => `${sku.skuName} × ${sku.missingQty}`)
                      .join(', ')}
                  </li>
                ))}
              </ul>
            </AlertDescription>
          </Alert>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm font-medium text-muted-foreground">
          {isLoadingTasks ? 'Загрузка задач...' : `Задач к сборке: ${tasks.length}`}
        </p>
        <Button
          type="button"
          onClick={onStartPicking}
          disabled={tasks.length === 0 || isLoadingTasks}
        >
          Начать сборку
        </Button>
      </div>
    </section>
  )
}
