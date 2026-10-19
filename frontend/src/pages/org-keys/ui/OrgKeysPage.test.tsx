// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { OrgKeysPage } from './OrgKeysPage'

const apiMocks = vi.hoisted(() => ({
  beginRegistration: vi.fn(),
  finishRegistration: vi.fn(),
  getAdminKeyExport: vi.fn(),
  getMyPasskey: vi.fn(),
  getOrganizationKeys: vi.fn(),
}))

vi.mock('@/features/passkey/api/passkeyApi', () => apiMocks)

const ORG_KEYS = {
  organization: { orgId: 'org-1', name: 'Acme' },
  members: [
    {
      userId: 'u1',
      username: 'boss',
      role: 'ADMIN',
      passkey: {
        credentialId: 'cred-1',
        keyId: '0xabcd1234ef567890',
        qx: '0x1',
        qy: '0x2',
        role: 'ADMIN',
        status: 'ACTIVE',
        createdAt: '2026-10-01T10:00:00Z',
      },
    },
    { userId: 'u2', username: 'op', role: 'OPERATOR', passkey: null },
  ],
}

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <OrgKeysPage />
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

describe('OrgKeysPage', () => {
  it('renders header and pending state while keys are loading', () => {
    apiMocks.getOrganizationKeys.mockReturnValue(new Promise(() => {}))

    renderPage()

    expect(screen.getByRole('heading', { name: 'Ключи организации' })).toBeTruthy()
    expect(screen.getByText('Загрузка…')).toBeTruthy()
  })

  it('renders members table and admin key download for loaded data', async () => {
    apiMocks.getOrganizationKeys.mockResolvedValue(ORG_KEYS)

    renderPage()

    expect(
      await screen.findByRole('heading', { name: 'Ключи организации Acme' })
    ).toBeTruthy()
    expect(screen.getByText('boss')).toBeTruthy()
    expect(screen.getByText('Администратор')).toBeTruthy()
    expect(screen.getByText('Активен в цепи')).toBeTruthy()
    expect(screen.getByText('0xabcd…7890')).toBeTruthy()
    expect(screen.getByText('op')).toBeTruthy()
    expect(screen.getByText('Оператор')).toBeTruthy()
    expect(screen.getByText('Нет ключа')).toBeTruthy()
    expect(screen.getByText('Скачать ключ админа')).toBeTruthy()
  })

  it('renders empty state when organization has no members', async () => {
    apiMocks.getOrganizationKeys.mockResolvedValue({
      organization: { orgId: 'org-1', name: 'Acme' },
      members: [],
    })

    renderPage()

    expect(await screen.findByText('В организации нет сотрудников.')).toBeTruthy()
    expect(screen.getByText('Скачать ключ админа')).toBeTruthy()
  })

  it('renders error alert when keys request fails', async () => {
    apiMocks.getOrganizationKeys.mockRejectedValue(new Error('Сервер недоступен'))

    renderPage()

    expect(await screen.findByText('Сервер недоступен')).toBeTruthy()
    expect(screen.queryByText('Скачать ключ админа')).toBeNull()
  })
})
