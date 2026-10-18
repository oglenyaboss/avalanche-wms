import { describe, expect, it } from 'vitest'

import { navigationItems, visibleNavigationItems } from './navigationItems'

const keyLabels = ['Мой ключ', 'Ключи организации']

function visibleKeyLabels(role: Parameters<typeof visibleNavigationItems>[1]) {
  return visibleNavigationItems(navigationItems, role)
    .map((item) => item.label)
    .filter((label) => keyLabels.includes(label))
}

describe('visibleNavigationItems', () => {
  it('shows both passkey items to ADMIN', () => {
    expect(visibleKeyLabels('ADMIN')).toEqual(keyLabels)
  })

  it('shows only "Мой ключ" to OPERATOR', () => {
    expect(visibleKeyLabels('OPERATOR')).toEqual(['Мой ключ'])
  })

  it('hides passkey items from CUSTOMER and anonymous users', () => {
    expect(visibleKeyLabels('CUSTOMER')).toEqual([])
    expect(visibleKeyLabels(undefined)).toEqual([])
  })

  it('keeps items without roles visible for everyone', () => {
    const dashboard = navigationItems[0].label
    expect(visibleNavigationItems(navigationItems, undefined).map((i) => i.label)).toContain(dashboard)
  })
})
