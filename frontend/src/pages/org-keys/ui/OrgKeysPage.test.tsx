// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { OrgKeysPage } from './OrgKeysPage'

const apiMocks = vi.hoisted(() => ({
  beginRegistration: vi.fn(),
  finishRegistration: vi.fn(),
  getAdminKeyExport: vi.fn(),
  getMyPasskey: vi.fn(),
  getOrganizationKeys: vi.fn(),
  rejectKey: vi.fn(),
  draftKeyConfirmation: vi.fn(),
  draftKeyRevoke: vi.fn(),
  submitKeyConfirmation: vi.fn(),
}))

vi.mock('@/features/passkey/api/passkeyApi', () => apiMocks)

const webauthnMocks = vi.hoisted(() => ({
  getAssertion: vi.fn(),
}))

vi.mock('@/features/passkey/model/webauthn', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/passkey/model/webauthn')>()),
  getAssertion: webauthnMocks.getAssertion,
}))

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

  it('rejects a pending operator key', async () => {
    apiMocks.getOrganizationKeys.mockResolvedValue({
      organization: { orgId: 'org-1', name: 'Acme' },
      members: [
        {
          userId: 'u2',
          username: 'op',
          role: 'OPERATOR',
          passkey: {
            credentialId: 'cred-op',
            keyId: '0xfeed000000000000beef',
            qx: '0x1',
            qy: '0x2',
            role: 'OPERATOR',
            status: 'PENDING',
            createdAt: '2026-10-01T10:00:00Z',
          },
          confirmation: null,
        },
      ],
    })
    apiMocks.rejectKey.mockResolvedValue(undefined)

    renderPage()

    expect(
      await screen.findByRole('button', { name: 'Подтвердить ключ' })
    ).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Отклонить' }))

    await waitFor(() =>
      expect(apiMocks.rejectKey).toHaveBeenCalledWith('0xfeed000000000000beef')
    )
    await waitFor(() =>
      expect(apiMocks.getOrganizationKeys.mock.calls.length).toBeGreaterThanOrEqual(2)
    )
  })

  it('offers key revocation only for active operator keys', async () => {
    apiMocks.getOrganizationKeys.mockResolvedValue({
      organization: { orgId: 'org-1', name: 'Acme' },
      members: [
        {
          userId: 'u1',
          username: 'boss',
          role: 'ADMIN',
          passkey: {
            credentialId: 'cred-admin',
            keyId: '0xabcd000000000000ef12',
            qx: '0x1',
            qy: '0x2',
            role: 'ADMIN',
            status: 'ACTIVE',
            createdAt: '2026-10-01T10:00:00Z',
          },
          confirmation: null,
        },
        {
          userId: 'u2',
          username: 'op-active',
          role: 'OPERATOR',
          passkey: {
            credentialId: 'cred-active',
            keyId: '0xaaaa0000000000000001',
            qx: '0x1',
            qy: '0x2',
            role: 'OPERATOR',
            status: 'ACTIVE',
            createdAt: '2026-10-02T10:00:00Z',
          },
          confirmation: null,
        },
        {
          userId: 'u3',
          username: 'op-pending',
          role: 'OPERATOR',
          passkey: {
            credentialId: 'cred-pending',
            keyId: '0xaaaa0000000000000002',
            qx: '0x1',
            qy: '0x2',
            role: 'OPERATOR',
            status: 'PENDING',
            createdAt: '2026-10-03T10:00:00Z',
          },
          confirmation: null,
        },
        {
          userId: 'u4',
          username: 'op-sent',
          role: 'OPERATOR',
          passkey: {
            credentialId: 'cred-sent',
            keyId: '0xaaaa0000000000000003',
            qx: '0x1',
            qy: '0x2',
            role: 'OPERATOR',
            status: 'ACTIVE',
            createdAt: '2026-10-04T10:00:00Z',
          },
          confirmation: { actionId: 'a-6', actionType: 6, state: 'SENT', rejectReason: null },
        },
      ],
    })

    renderPage()

    expect(await screen.findByText('op-active')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Отозвать ключ' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Подтвердить ключ' })).toBeTruthy()
    expect(screen.getAllByRole('button', { name: 'Отклонить' })).toHaveLength(1)
    expect(screen.getByText('Отзыв отправлен в цепь')).toBeTruthy()
    // One revoke button total: neither the admin key nor the SENT-revoke row offers one.
    expect(screen.getAllByRole('button', { name: 'Отозвать ключ' })).toHaveLength(1)
  })

  it('revokes an active operator key through the confirmation dialog', async () => {
    apiMocks.getOrganizationKeys.mockResolvedValue({
      organization: { orgId: 'org-1', name: 'Acme' },
      members: [
        {
          userId: 'u2',
          username: 'op',
          role: 'OPERATOR',
          passkey: {
            credentialId: 'cred-op',
            keyId: '0xfeed000000000000beef',
            qx: '0x1',
            qy: '0x2',
            role: 'OPERATOR',
            status: 'ACTIVE',
            createdAt: '2026-10-01T10:00:00Z',
          },
          confirmation: null,
        },
      ],
    })
    apiMocks.draftKeyRevoke.mockResolvedValue({
      actionId: 'action-6',
      actionType: 6,
      challenge: 'AQID',
      rpId: 'localhost',
      allowCredentials: [{ type: 'public-key', id: 'AQID' }],
      userVerification: 'required',
      timeout: 60000,
    })
    const assertion = {
      credentialId: 'cred-op',
      authenticatorData: 'AQ',
      clientDataJson: 'CA',
      signature: 'SIG',
    }
    webauthnMocks.getAssertion.mockResolvedValue(assertion)
    apiMocks.submitKeyConfirmation.mockResolvedValue({
      actionId: 'action-6',
      actionType: 6,
      state: 'SENT',
      rejectReason: null,
    })

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Отозвать ключ' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отозвать' }))

    await waitFor(() =>
      expect(apiMocks.draftKeyRevoke).toHaveBeenCalledWith('0xfeed000000000000beef')
    )
    await waitFor(() => expect(webauthnMocks.getAssertion).toHaveBeenCalled())
    await waitFor(() =>
      expect(apiMocks.submitKeyConfirmation).toHaveBeenCalledWith('action-6', assertion)
    )
  })

  it('shows the webauthn error text when the revoke signature is dismissed', async () => {
    apiMocks.getOrganizationKeys.mockResolvedValue({
      organization: { orgId: 'org-1', name: 'Acme' },
      members: [
        {
          userId: 'u2',
          username: 'op',
          role: 'OPERATOR',
          passkey: {
            credentialId: 'cred-op',
            keyId: '0xfeed000000000000beef',
            qx: '0x1',
            qy: '0x2',
            role: 'OPERATOR',
            status: 'ACTIVE',
            createdAt: '2026-10-01T10:00:00Z',
          },
          confirmation: null,
        },
      ],
    })
    apiMocks.draftKeyRevoke.mockResolvedValue({
      actionId: 'action-6',
      actionType: 6,
      challenge: 'AQID',
      rpId: 'localhost',
      allowCredentials: [{ type: 'public-key', id: 'AQID' }],
      userVerification: 'required',
      timeout: 60000,
    })
    webauthnMocks.getAssertion.mockRejectedValue(
      new DOMException('user dismissed', 'NotAllowedError')
    )

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Отозвать ключ' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отозвать' }))

    expect(
      await screen.findByText('Подтверждение отменено или истекло время ожидания')
    ).toBeTruthy()
  })
})
