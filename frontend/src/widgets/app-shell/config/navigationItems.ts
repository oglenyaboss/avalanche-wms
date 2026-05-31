import { routes } from '@/shared/config'

type NavigationItem = {
  label: string
  title: string
  to?: string
}

export const navigationItems: NavigationItem[] = [
  {
    label: 'Приемка на воротах',
    title: 'Сценарий приемки товара на воротах',
    to: routes.receivingGate,
  },
  {
    label: 'Приемка на столах',
    title: 'Сценарий приемки товара на рабочих столах',
    to: routes.receivingTable,
  },
  {
    label: 'Раскладка',
    title: 'Сценарий раскладки товаров по местам хранения',
  },
  {
    label: 'Сборка',
    title: 'Сценарий сборки заказа',
  },
  {
    label: 'Отгрузка',
    title: 'Сценарий отгрузки заказа',
  },
  {
    label: 'Аналитика',
    title: 'Раздел аналитики склада',
  },
]
