// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { MyKeyPage } from './MyKeyPage'

const apiMocks = vi.hoisted(() => ({
  beginRegistration: vi.fn(),
  finishRegistration: vi.fn(),
  getAdminKeyExport: vi.fn(),
  getMyPasskey: vi.fn(),
  getOrganizationKeys: vi.fn(),
}))

vi.mock('@/features/passkey/api/passkeyApi', () => apiMocks)

const ACTIVE_PASSKEY = {
  credentialId: 'cred-1',
  keyId: '0xabcd1234ef567890',
  qx: '0x1',
  qy: '0x2',
  role: 'ADMIN',
  status: 'ACTIVE',
  createdAt: '2026-10-01T10:00:00Z',
}

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <MyKeyPage />
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

describe('MyKeyPage', () => {
  it('renders header and pending state while the key is loading', () => {
    apiMocks.getMyPasskey.mockReturnValue(new Promise(() => {}))

    renderPage()

    expect(screen.getByRole('heading', { name: 'Мой ключ' })).toBeTruthy()
    expect(screen.getByText('Загрузка…')).toBeTruthy()
  })

  it('renders empty state with register button when no key exists', async () => {
    apiMocks.getMyPasskey.mockResolvedValue(null)

    renderPage()

    expect(await screen.findByText('Ключа пока нет.')).toBeTruthy()
    expect(screen.getByText('Создать ключ')).toBeTruthy()
  })

  it('renders key details for an active passkey without register button', async () => {
    apiMocks.getMyPasskey.mockResolvedValue(ACTIVE_PASSKEY)

    renderPage()

    expect(await screen.findByText('Активен в цепи')).toBeTruthy()
    expect(screen.getByText('0xabcd…7890')).toBeTruthy()
    expect(screen.queryByText('Создать ключ')).toBeNull()
  })

  it('renders error alert when the key request fails', async () => {
    apiMocks.getMyPasskey.mockRejectedValue(new Error('Сервер недоступен'))

    renderPage()

    expect(await screen.findByText('Сервер недоступен')).toBeTruthy()
    expect(screen.queryByText('Создать ключ')).toBeNull()
  })
})
