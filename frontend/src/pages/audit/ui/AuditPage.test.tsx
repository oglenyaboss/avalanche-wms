// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Hex } from 'viem'

import { useAuditSettings, type AuditStage, type ProductAudit } from '@/features/audit'

import { AuditPage } from './AuditPage'

const apiMocks = vi.hoisted(() => ({
  getProductAudit: vi.fn(),
  mapProductAudit: vi.fn(),
}))

const chainMocks = vi.hoisted(() => ({
  createChainReader: vi.fn(),
  bytes16ToUuid: vi.fn(),
}))

const modelMocks = vi.hoisted(() => ({
  auditProduct: vi.fn(),
}))

vi.mock('@/features/audit/api/auditApi', () => apiMocks)
vi.mock('@/features/audit/api/chainReader', () => chainMocks)
vi.mock('@/features/audit/model/auditProduct', () => modelMocks)

const RPC_URL = 'http://localhost:9699/ext/bc/C/rpc'
const CONTRACT_ADDRESS = '0x' + 'ab'.repeat(20)
const PRODUCT_KEY = '01929c5e-6f00-7000-8000-000000000103'

const PRODUCT: ProductAudit = {
  productId: PRODUCT_KEY,
  itemId: '0x4ac4a515f579f79cce4ba71ea357787f5208aa5ca7c4f612201be0dd2673ee18' as Hex,
  actions: [],
}

function appliedStage(overrides: Partial<AuditStage> = {}): AuditStage {
  return {
    actionId: '01929c5e-6f00-7000-8000-000000001003',
    actionType: 2,
    status: 'applied',
    operator: 'Оператор',
    operatorId: '01929c5e-6f00-7000-8000-0000000000b1',
    orgId: '01929c5e-6f00-7000-8000-0000000000a1',
    orgName: 'Склад №1',
    blockNumber: 42n,
    blockTime: new Date('2025-10-18T10:01:00Z'),
    txHash: `0x${'aa'.repeat(32)}`,
    rejectReason: null,
    detailsHash: 'ok',
    items: 'ok',
    signer: 'ok',
    signature: 'ok',
    problems: [],
    ...overrides,
  }
}

function setupSettings() {
  try {
    localStorage.setItem('audit.rpcUrl', RPC_URL)
    localStorage.setItem('audit.contractAddress', CONTRACT_ADDRESS)
  } catch {
    // no storage in this environment — the store state is the source of truth
  }
  useAuditSettings.setState({ rpcUrl: RPC_URL, contractAddress: CONTRACT_ADDRESS })
}

function renderPage(key?: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[key ? `/audit?key=${key}` : '/audit']}>
        <AuditPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
  try {
    localStorage.clear()
  } catch {
    // no storage in this environment
  }
  useAuditSettings.setState({ rpcUrl: '', contractAddress: '' })
})

describe('AuditPage', () => {
  it('shows stage checks, executor and organization for an applied stage', async () => {
    setupSettings()
    apiMocks.getProductAudit.mockResolvedValue(PRODUCT)
    modelMocks.auditProduct.mockResolvedValue([appliedStage()])

    renderPage(PRODUCT_KEY)

    expect(await screen.findByText('Раскладка')).toBeTruthy()
    expect(screen.getByLabelText('Хеш деталей: совпадает')).toBeTruthy()
    expect(screen.getByLabelText('Подпись: совпадает')).toBeTruthy()
    expect(screen.getByText('Оператор')).toBeTruthy()
    expect(screen.getByText('Склад №1')).toBeTruthy()
  })

  it('shows the details-hash mismatch mark and the problem text', async () => {
    setupSettings()
    apiMocks.getProductAudit.mockResolvedValue(PRODUCT)
    modelMocks.auditProduct.mockResolvedValue([
      appliedStage({
        detailsHash: 'mismatch',
        problems: ['Хеш деталей из БД не совпадает с цепью'],
      }),
    ])

    renderPage(PRODUCT_KEY)

    expect(await screen.findByLabelText('Хеш деталей: не совпадает')).toBeTruthy()
    expect(screen.getByText('Хеш деталей из БД не совпадает с цепью')).toBeTruthy()
  })

  it('hints at incomplete settings without fetching', async () => {
    try {
      localStorage.clear()
    } catch {
      // no storage in this environment
    }
    useAuditSettings.setState({ rpcUrl: '', contractAddress: '' })

    renderPage(PRODUCT_KEY)

    expect(await screen.findByText('Укажите RPC-узел и адрес контракта')).toBeTruthy()
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(apiMocks.getProductAudit).not.toHaveBeenCalled()
  })

  it('shows the not-found message on a backend 404', async () => {
    setupSettings()
    apiMocks.getProductAudit.mockRejectedValue({
      isAxiosError: true,
      message: 'Request failed with status code 404',
      response: { status: 404, data: { error: { message: 'Товар не найден' } } },
    })

    renderPage(PRODUCT_KEY)

    expect(await screen.findByText('Товар не найден.')).toBeTruthy()
  })
})
