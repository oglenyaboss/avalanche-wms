// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type {
  BufferScan,
  PickedProductResult,
  StoragePlacement,
} from '@/entities/putaway'
import type { PartAssertion, SignedOperationDraft } from '@/entities/signed-action'

const apiMocks = vi.hoisted(() => ({
  draftPlaceToStorageBin: vi.fn(),
  scanBuffer: vi.fn(),
  scanProduct: vi.fn(),
  submitPlaceToStorageBin: vi.fn(),
}))

vi.mock('../api/putawayApi', () => apiMocks)

const signMocks = vi.hoisted(() => ({
  signOperation: vi.fn(),
}))

vi.mock('@/entities/signed-action', () => signMocks)

import { usePutaway } from './usePutaway'

const BUFFER_SCAN: BufferScan = {
  bufferBinId: '01929c5e-buf-1',
  bufferCode: 'BUFFER-01',
  products: [
    {
      productId: '01929c5e-p-1',
      skuName: 'Кроссовки',
      qrCode: 'QR-1',
      status: 'RECEIVED',
    },
    {
      productId: '01929c5e-p-2',
      skuName: 'Футболка',
      qrCode: 'QR-2',
      status: 'RECEIVED',
    },
  ],
  totalProducts: 2,
}

function picked(overrides: Partial<PickedProductResult>): PickedProductResult {
  return {
    productId: '01929c5e-p-1',
    skuName: 'Кроссовки',
    qrCode: 'QR-1',
    cartSize: 2,
    ...overrides,
  }
}

const DRAFT: SignedOperationDraft = {
  operationId: '01929c5e-op-1',
  actionType: 2,
  signerKeyId: '0xabc',
  rpId: 'localhost',
  allowCredentials: [{ type: 'public-key', id: 'AQID' }],
  userVerification: 'required',
  timeout: 60000,
  parts: [
    {
      actionId: '01929c5e-a-1',
      itemIds: ['01929c5e-p-1', '01929c5e-p-2'],
      detailsHash: '0x1',
      challenge: 'AQID',
    },
  ],
}

const ASSERTIONS: PartAssertion[] = [
  {
    actionId: '01929c5e-a-1',
    credentialId: 'AQ',
    authenticatorData: 'Ag',
    clientDataJson: 'Aw',
    signature: 'BA',
  },
]

const PLACEMENT: StoragePlacement = {
  storageBinId: '01929c5e-bin-1',
  storageBinCode: 'A-01-12',
  productsPlaced: 2,
  outboxEventsCreated: 1,
  operationId: '01929c5e-op-1',
}

// Minimal AxiosError-shaped fixture; axios.isAxiosError only checks the flag.
function axiosError(status: number, data: unknown) {
  return {
    isAxiosError: true,
    response: { status, data },
  }
}

function envelope(code: string, message: string) {
  return { success: false, data: null, error: { code, message } }
}

function renderPutaway() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return renderHook(() => usePutaway(), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
}

type UsePutawayResult = ReturnType<typeof usePutaway>

// Walk the flow to the storage phase with both products picked.
async function pickTwoAndGoToStorage(result: { current: UsePutawayResult }) {
  await act(async () => {
    await result.current.submitBuffer('01929c5e-buf-1')
  })
  apiMocks.scanProduct.mockResolvedValueOnce(
    picked({ productId: '01929c5e-p-1', qrCode: 'QR-1' }),
  )
  await act(async () => {
    await result.current.submitPick('QR-1')
  })
  apiMocks.scanProduct.mockResolvedValueOnce(
    picked({ productId: '01929c5e-p-2', skuName: 'Футболка', qrCode: 'QR-2' }),
  )
  await act(async () => {
    await result.current.submitPick('QR-2')
  })
  act(() => {
    result.current.goToStorage()
  })
}

afterEach(() => {
  vi.resetAllMocks()
})

describe('usePutaway storage bin scan', () => {
  it('drafts, signs and submits the whole unplaced cart in one action', async () => {
    apiMocks.scanBuffer.mockResolvedValue(BUFFER_SCAN)
    apiMocks.draftPlaceToStorageBin.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockResolvedValue(ASSERTIONS)
    apiMocks.submitPlaceToStorageBin.mockResolvedValue(PLACEMENT)

    const { result } = renderPutaway()
    await pickTwoAndGoToStorage(result)

    await act(async () => {
      await result.current.submitStorageBin('01929c5e-bin-1')
    })

    expect(apiMocks.draftPlaceToStorageBin).toHaveBeenCalledWith({
      productIds: ['01929c5e-p-1', '01929c5e-p-2'],
      storageBinId: '01929c5e-bin-1',
    })
    expect(signMocks.signOperation).toHaveBeenCalledWith(DRAFT)
    expect(apiMocks.submitPlaceToStorageBin).toHaveBeenCalledWith(
      '01929c5e-op-1',
      ASSERTIONS
    )
    expect(result.current.state.placedProductIds).toEqual([
      '01929c5e-p-1',
      '01929c5e-p-2',
    ])
    expect(result.current.state.lastPlacement).toEqual(PLACEMENT)
    expect(result.current.state.errorMessage).toBeNull()
  })

  it('shows the signing error text when the authenticator cancels the prompt', async () => {
    apiMocks.scanBuffer.mockResolvedValue(BUFFER_SCAN)
    apiMocks.draftPlaceToStorageBin.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockRejectedValue(
      new DOMException('denied', 'NotAllowedError')
    )

    const { result } = renderPutaway()
    await pickTwoAndGoToStorage(result)

    await act(async () => {
      await result.current.submitStorageBin('01929c5e-bin-1')
    })

    expect(result.current.state.errorMessage).toBe(
      'Подтверждение отменено или истекло время ожидания'
    )
    expect(apiMocks.submitPlaceToStorageBin).not.toHaveBeenCalled()
    expect(result.current.state.placedProductIds).toEqual([])
  })

  it('surfaces the DRAFT_STALE message and keeps the bin scan available', async () => {
    apiMocks.scanBuffer.mockResolvedValue(BUFFER_SCAN)
    apiMocks.draftPlaceToStorageBin.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockResolvedValue(ASSERTIONS)
    apiMocks.submitPlaceToStorageBin.mockRejectedValue(
      axiosError(
        409,
        envelope(
          'DRAFT_STALE',
          'Состав корзины изменился, отсканируйте ячейку ещё раз',
        ),
      ),
    )

    const { result } = renderPutaway()
    await pickTwoAndGoToStorage(result)

    await act(async () => {
      await result.current.submitStorageBin('01929c5e-bin-1')
    })

    expect(result.current.state.errorMessage).toBe(
      'Состав корзины изменился, отсканируйте ячейку ещё раз'
    )
    expect(result.current.state.phase).toBe('storage')
    expect(result.current.state.placedProductIds).toEqual([])
  })

  it('shows the server error and stays in storage on STORAGE_BIN_NOT_FOUND', async () => {
    apiMocks.scanBuffer.mockResolvedValue(BUFFER_SCAN)
    apiMocks.draftPlaceToStorageBin.mockRejectedValue(
      axiosError(404, envelope('STORAGE_BIN_NOT_FOUND', 'Ячейка не найдена')),
    )

    const { result } = renderPutaway()
    await pickTwoAndGoToStorage(result)

    await act(async () => {
      await result.current.submitStorageBin('01929c5e-bin-9')
    })

    expect(result.current.state.errorMessage).toBe('Ячейка не найдена')
    expect(result.current.state.phase).toBe('storage')
    expect(result.current.state.placedProductIds).toEqual([])
  })
})
