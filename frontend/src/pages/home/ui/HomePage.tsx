import {
  Analytics01Icon,
  ArrowRight01Icon,
  DeliveryTruck01Icon,
  InboxIcon,
  Layers01Icon,
  PackageProcessIcon,
  TruckIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import type { IconSvgElement } from '@hugeicons/react'
import { Link } from 'react-router'

import { routes } from '@/shared/config'

type QuickAction = {
  label: string
  description: string
  to: string
  icon: IconSvgElement
}

const quickActions: QuickAction[] = [
  {
    label: 'Приемка на воротах',
    description: 'Приём товара от перевозчика по ТТН',
    to: routes.receivingGate,
    icon: DeliveryTruck01Icon,
  },
  {
    label: 'Приемка на столах',
    description: 'Разбор грузомест и сверка SKU на столах',
    to: routes.receivingTable,
    icon: InboxIcon,
  },
  {
    label: 'Раскладка',
    description: 'Размещение товара по местам хранения',
    to: routes.putaway,
    icon: Layers01Icon,
  },
  {
    label: 'Сборка',
    description: 'Комплектация заказов на отгрузку',
    to: routes.assembly,
    icon: PackageProcessIcon,
  },
  {
    label: 'Отгрузка',
    description: 'Передача заказов водителю и фиксация',
    to: routes.shipping,
    icon: TruckIcon,
  },
  {
    label: 'Аналитика',
    description: 'Метрики склада и состояние блокчейн-аудита',
    to: routes.analytics,
    icon: Analytics01Icon,
  },
]

export function HomePage() {
  return (
    <div className="mx-auto w-full max-w-6xl space-y-8">
      <section className="relative overflow-hidden rounded-2xl bg-primary px-8 py-10 text-primary-foreground shadow-card">
        <div
          aria-hidden
          className="bg-dotgrid-invert pointer-events-none absolute inset-0"
        />
        <div
          aria-hidden
          className="pointer-events-none absolute -top-20 -right-20 size-72 rounded-full bg-status-committed/20 blur-3xl"
        />
        <div className="relative max-w-2xl">
          <span className="inline-flex items-center gap-2 rounded-full bg-white/10 px-3 py-1 text-xs font-medium text-primary-foreground/90">
            <span className="size-1.5 animate-pulse rounded-full bg-status-committed" />
            Avalanche Subnet · аудит в реальном времени
          </span>
          <h2 className="mt-4 text-3xl font-semibold tracking-tight text-balance">
            Складские операции, подтверждённые в блокчейне
          </h2>
          <p className="mt-3 leading-relaxed text-primary-foreground/75">
            Каждое движение товара — от приёмки до отгрузки — фиксируется как
            неизменяемое событие в реестре. Выберите сценарий, чтобы начать
            работу.
          </p>
        </div>
      </section>

      <section>
        <h3 className="mb-4 text-xs font-medium tracking-[0.14em] text-muted-foreground uppercase">
          Сценарии
        </h3>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {quickActions.map((action) => (
            <Link
              key={action.to}
              to={action.to}
              className="group flex flex-col gap-3 rounded-xl border border-border bg-card p-5 shadow-soft transition-all duration-200 hover:-translate-y-0.5 hover:shadow-card"
            >
              <span className="grid size-11 place-items-center rounded-xl bg-secondary text-foreground transition-colors group-hover:bg-primary group-hover:text-primary-foreground">
                <HugeiconsIcon
                  icon={action.icon}
                  strokeWidth={1.8}
                  className="size-5"
                />
              </span>
              <div>
                <div className="flex items-center gap-1.5">
                  <span className="font-medium text-foreground">
                    {action.label}
                  </span>
                  <HugeiconsIcon
                    icon={ArrowRight01Icon}
                    strokeWidth={2}
                    className="size-4 -translate-x-1 text-muted-foreground opacity-0 transition-all duration-200 group-hover:translate-x-0 group-hover:opacity-100"
                  />
                </div>
                <p className="mt-1 text-sm text-muted-foreground">
                  {action.description}
                </p>
              </div>
            </Link>
          ))}
        </div>
      </section>
    </div>
  )
}
