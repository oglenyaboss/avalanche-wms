import {
  Analytics01Icon,
  DeliveryTruck01Icon,
  Key01Icon,
  PackageMovingIcon,
  PackageReceiveIcon,
  Route01Icon,
  Shield01Icon,
  ShoppingBasket01Icon,
  TruckDeliveryIcon,
  UserGroupIcon,
} from "@hugeicons/core-free-icons";

import type { Role } from "@/lib/auth/roles";

export type NavItem = {
  href: string;
  title: string;
  icon: typeof Key01Icon;
  roles: Role[];
};

export type NavSection = { title: string; items: NavItem[] };

export const navigation: NavSection[] = [
  {
    title: "Склад",
    items: [
      { href: "/receiving/gate", title: "Приёмка у ворот", icon: TruckDeliveryIcon, roles: ["OPERATOR"] },
      { href: "/receiving/table", title: "Стол приёмки", icon: PackageReceiveIcon, roles: ["OPERATOR"] },
      { href: "/putaway", title: "Раскладка", icon: PackageMovingIcon, roles: ["OPERATOR"] },
      { href: "/assembly", title: "Сборка", icon: ShoppingBasket01Icon, roles: ["OPERATOR"] },
      { href: "/shipping", title: "Отгрузка", icon: DeliveryTruck01Icon, roles: ["OPERATOR"] },
    ],
  },
  {
    title: "Контроль",
    items: [
      { href: "/trace", title: "Трассировка товара", icon: Route01Icon, roles: ["OPERATOR", "ADMIN"] },
      { href: "/analytics", title: "Аналитика", icon: Analytics01Icon, roles: ["OPERATOR", "ADMIN"] },
      { href: "/audit", title: "Аудит", icon: Shield01Icon, roles: ["AUDITOR", "ADMIN"] },
    ],
  },
  {
    title: "Ключи",
    items: [
      { href: "/passkey", title: "Мой ключ", icon: Key01Icon, roles: ["OPERATOR", "ADMIN"] },
      { href: "/organization/keys", title: "Ключи организации", icon: UserGroupIcon, roles: ["ADMIN"] },
    ],
  },
];

export function navigationFor(role: Role): NavSection[] {
  return navigation
    .map((section) => ({ ...section, items: section.items.filter((item) => item.roles.includes(role)) }))
    .filter((section) => section.items.length > 0);
}

export function navItemFor(pathname: string): NavItem | undefined {
  return navigation
    .flatMap((section) => section.items)
    .find((item) => pathname === item.href || pathname.startsWith(`${item.href}/`));
}
