import {
  Analytics01Icon,
  DashboardSquare01Icon,
  DeliveryTruck01Icon,
  InboxIcon,
  Key01Icon,
  Layers01Icon,
  PackageProcessIcon,
  Route01Icon,
  TruckIcon,
  UserGroupIcon,
} from '@hugeicons/core-free-icons'
import type { IconSvgElement } from '@hugeicons/react'

import type { UserRole } from '@/entities/user'
import { routes } from '@/shared/config'

export type NavigationItem = {
  label: string
  title: string
  to: string
  icon: IconSvgElement
  roles?: UserRole[]
}

export const navigationItems: NavigationItem[] = [
  {
    label: 'Дашборд',
    title: 'Обзор склада',
    to: routes.home,
    icon: DashboardSquare01Icon,
  },
  {
    label: 'Приемка на воротах',
    title: 'Сценарий приемки товара на воротах',
    to: routes.receivingGate,
    icon: DeliveryTruck01Icon,
  },
  {
    label: 'Приемка на столах',
    title: 'Сценарий приемки товара на рабочих столах',
    to: routes.receivingTable,
    icon: InboxIcon,
  },
  {
    label: 'Раскладка',
    title: 'Сценарий раскладки товаров по местам хранения',
    to: routes.putaway,
    icon: Layers01Icon,
  },
  {
    label: 'Сборка',
    title: 'Сценарий сборки заказа',
    to: routes.assembly,
    icon: PackageProcessIcon,
  },
  {
    label: 'Отгрузка',
    title: 'Сценарий отгрузки заказа',
    to: routes.shipping,
    icon: TruckIcon,
  },
  {
    label: 'Аналитика',
    title: 'Аналитика склада и блокчейн-аудит',
    to: routes.analytics,
    icon: Analytics01Icon,
  },
  {
    label: 'Трассировка',
    title: 'История товара и его подтверждения в блокчейне',
    to: routes.trace,
    icon: Route01Icon,
  },
  {
    label: 'Мой ключ',
    title: 'Passkey для подписи складских операций',
    to: routes.myKey,
    icon: Key01Icon,
    roles: ['ADMIN', 'OPERATOR'],
  },
  {
    label: 'Ключи организации',
    title: 'Сотрудники организации и их ключи',
    to: routes.orgKeys,
    icon: UserGroupIcon,
    roles: ['ADMIN'],
  },
]

export function visibleNavigationItems(
  items: NavigationItem[],
  role: UserRole | undefined
): NavigationItem[] {
  return items.filter((item) => !item.roles || (role !== undefined && item.roles.includes(role)))
}
