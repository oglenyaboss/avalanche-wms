// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type {
  OpenedCargoplace,
  CloseCargoplaceSummary,
} from '@/entities/cargoplace'

const apiMocks = vi.hoisted(() => ({
  closeBox: vi.fn(),
  draftCloseCargoplace: vi.fn(),
  scanBox: vi.fn(),
  scanBuffer: vi.fn(),
  scanQr: vi.fn(),
  scanSku: vi.fn(),
  scanTableCargoplace: vi.fn(),
  submitCloseCargoplace: vi.fn(),
}))

vi.mock('../api/receivingTableApi', () => apiMocks)

const signMocks = vi.hoisted(() => ({
  signOperation: vi.fn(),
}))

vi.mock('./signOperation', () => signMocks)

import { useTableReceiving } from './useTableReceiving'

const OPENED_CARGOPLACE: OpenedCargoplace = {
  cargoplaceId: '01929c5e-cp-1',
  cargoplaceCode: 'CP-1',
  status: 'TABLE_IN_PROGRESS',
  expectedSkus: [],
  totalExpected: 2,
}

const SUMMARY: CloseCargoplaceSummary = {
  productsReceived: 2,
  productsExpected: 2,
  shortage: 0,
  shortageBySku: [],
}

const DRAFT = {
  operationId: '01929c5e-op-1',
  actionType: 3,
  signerKeyId: '0xabc',
  rpId: 'localhost',
  allowCredentials: [{ type: 'public-key', id: 'AQID' }],
  userVerification: 'required' as const,
  timeout: 60000,
  parts: [{ actionId: 'a-1', itemIds: ['i-1'], detailsHash: '0x1', challenge: 'AQID' }],
}

const ASSERTIONS = [
  {
    actionId: 'a-1',
    credentialId: 'AQ',
    authenticatorData: 'Ag',
    clientDataJson: 'Aw',
    signature: 'BA',
  },
]

function renderReceiving() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return renderHook(() => useTableReceiving(), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
}

afterEach(() => {
  vi.resetAllMocks()
})

describe('useTableReceiving close-cargoplace flow', () => {
  it('drafts, signs and submits, then stores the summary with the operation id', async () => {
    apiMocks.scanTableCargoplace.mockResolvedValue(OPENED_CARGOPLACE)
    apiMocks.draftCloseCargoplace.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockResolvedValue(ASSERTIONS)
    apiMocks.submitCloseCargoplace.mockResolvedValue({
      cargoplaceId: '01929c5e-cp-1',
      status: 'TABLE_CLOSED',
      summary: SUMMARY,
      outboxEventsCreated: 2,
      operationId: '01929c5e-op-1',
    })

    const { result } = renderReceiving()

    await act(async () => {
      await result.current.submitCargoplace('01929c5e-cp-1')
    })
    expect(result.current.state.cargoplace).not.toBeNull()

    await act(async () => {
      await result.current.submitCloseCargoplace()
    })

    expect(apiMocks.draftCloseCargoplace).toHaveBeenCalledWith('01929c5e-cp-1')
    expect(signMocks.signOperation).toHaveBeenCalledWith(DRAFT)
    expect(apiMocks.submitCloseCargoplace).toHaveBeenCalledWith(
      '01929c5e-op-1',
      ASSERTIONS
    )
    expect(result.current.state.summary).toEqual(SUMMARY)
    expect(result.current.state.operationId).toBe('01929c5e-op-1')
    expect(result.current.state.errorMessage).toBeNull()
  })

  it('shows the signing error text when the authenticator cancels the prompt', async () => {
    apiMocks.scanTableCargoplace.mockResolvedValue(OPENED_CARGOPLACE)
    apiMocks.draftCloseCargoplace.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockRejectedValue(
      new DOMException('denied', 'NotAllowedError')
    )

    const { result } = renderReceiving()

    await act(async () => {
      await result.current.submitCargoplace('01929c5e-cp-1')
    })

    await act(async () => {
      await result.current.submitCloseCargoplace()
    })

    expect(result.current.state.errorMessage).toBe(
      'Подтверждение отменено или истекло время ожидания'
    )
    expect(result.current.state.summary).toBeNull()
    expect(apiMocks.submitCloseCargoplace).not.toHaveBeenCalled()
  })
})
