// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type {
  AssemblyTask,
  Destination,
  PickResult,
  ShippingBufferResult,
} from '@/entities/assembly'
import type { PartAssertion, SignedOperationDraft } from '@/entities/signed-action'

const apiMocks = vi.hoisted(() => ({
  allocate: vi.fn(),
  draftUnloadToShippingBuffer: vi.fn(),
  getDestinations: vi.fn(),
  getTasks: vi.fn(),
  pick: vi.fn(),
  submitUnloadToShippingBuffer: vi.fn(),
}))

vi.mock('../api/assemblyApi', () => apiMocks)

const signMocks = vi.hoisted(() => ({
  signOperation: vi.fn(),
}))

vi.mock('@/entities/signed-action', () => signMocks)

import { useAssembly } from './useAssembly'

const destinations: Destination[] = [
  {
    destinationId: 'dst-1',
    code: 'STORE-01',
    name: 'Магазин на Тверской',
    address: 'ул. Тверская, 1',
    warehouseId: 1,
  },
]

const tasks: AssemblyTask[] = [
  {
    taskId: 't-1',
    productId: 'p-1',
    qrCode: 'QR-1',
    skuName: 'Кроссовки',
    fromBinCode: 'M2-A-03',
    fromBinSection: 'A',
    orderNo: 'ORD-1',
  },
]

const PICK_RESULT: PickResult = { productId: 'p-1', cartSize: 1 }

const DRAFT: SignedOperationDraft = {
  operationId: 'op-1',
  actionType: 3,
  signerKeyId: '0xabc',
  rpId: 'localhost',
  allowCredentials: [{ type: 'public-key', id: 'AQID' }],
  userVerification: 'required',
  timeout: 60000,
  parts: [
    {
      actionId: 'a-1',
      itemIds: ['p-1'],
      detailsHash: '0x1',
      challenge: 'AQID',
    },
  ],
}

const ASSERTIONS: PartAssertion[] = [
  {
    actionId: 'a-1',
    credentialId: 'AQ',
    authenticatorData: 'Ag',
    clientDataJson: 'Aw',
    signature: 'BA',
  },
]

const UNLOAD: ShippingBufferResult = {
  bufferBinId: 'buf-1',
  productsPlaced: 1,
  ordersAssembled: 1,
  operationId: 'op-1',
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

function renderAssembly() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return renderHook(() => useAssembly(), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
}

type UseAssemblyResult = ReturnType<typeof useAssembly>

// Walk the flow to the buffer phase with one product picked: wait for the
// destinations query, select the store (loads its tasks), pick one product.
async function startTrip(result: { current: UseAssemblyResult }) {
  await waitFor(() =>
    expect(result.current.state.destinations).toEqual(destinations),
  )
  await act(async () => {
    result.current.selectDestination(destinations[0])
  })
  await waitFor(() => expect(result.current.state.tasks).toEqual(tasks))
  act(() => {
    result.current.startPicking()
  })
  await act(async () => {
    await result.current.submitPick('QR-1')
  })
  act(() => {
    result.current.goToBuffer()
  })
}

afterEach(() => {
  vi.resetAllMocks()
})

describe('useAssembly shipping-buffer scan', () => {
  it('drafts, signs and submits the whole cart in one signed action', async () => {
    apiMocks.getDestinations.mockResolvedValue(destinations)
    apiMocks.getTasks.mockResolvedValue(tasks)
    apiMocks.pick.mockResolvedValue(PICK_RESULT)
    apiMocks.draftUnloadToShippingBuffer.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockResolvedValue(ASSERTIONS)
    apiMocks.submitUnloadToShippingBuffer.mockResolvedValue(UNLOAD)

    const { result } = renderAssembly()
    await startTrip(result)

    await act(async () => {
      await result.current.submitBuffer('buf-1')
    })

    expect(apiMocks.draftUnloadToShippingBuffer).toHaveBeenCalledWith('buf-1')
    expect(signMocks.signOperation).toHaveBeenCalledWith(DRAFT)
    expect(apiMocks.submitUnloadToShippingBuffer).toHaveBeenCalledWith(
      'op-1',
      ASSERTIONS
    )
    // Back on the store panel (multi-trip) with the unload result for the
    // chain-status panel.
    expect(result.current.state.phase).toBe('destination')
    expect(result.current.state.lastBufferResult).toEqual(UNLOAD)
    expect(result.current.state.successMessage).toContain('Размещено товаров: 1')
    expect(result.current.state.errorMessage).toBeNull()
  })

  it('shows the signing error text when the authenticator cancels the prompt', async () => {
    apiMocks.getDestinations.mockResolvedValue(destinations)
    apiMocks.getTasks.mockResolvedValue(tasks)
    apiMocks.pick.mockResolvedValue(PICK_RESULT)
    apiMocks.draftUnloadToShippingBuffer.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockRejectedValue(
      new DOMException('denied', 'NotAllowedError')
    )

    const { result } = renderAssembly()
    await startTrip(result)

    await act(async () => {
      await result.current.submitBuffer('buf-1')
    })

    expect(result.current.state.errorMessage).toBe(
      'Подтверждение отменено или истекло время ожидания'
    )
    expect(apiMocks.submitUnloadToShippingBuffer).not.toHaveBeenCalled()
    expect(result.current.state.phase).toBe('buffer')
  })

  it('surfaces the DRAFT_STALE message and keeps the buffer scan available', async () => {
    apiMocks.getDestinations.mockResolvedValue(destinations)
    apiMocks.getTasks.mockResolvedValue(tasks)
    apiMocks.pick.mockResolvedValue(PICK_RESULT)
    apiMocks.draftUnloadToShippingBuffer.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockResolvedValue(ASSERTIONS)
    apiMocks.submitUnloadToShippingBuffer.mockRejectedValue(
      axiosError(
        409,
        envelope(
          'DRAFT_STALE',
          'Состав корзины изменился, отсканируйте буфер ещё раз',
        ),
      ),
    )

    const { result } = renderAssembly()
    await startTrip(result)

    await act(async () => {
      await result.current.submitBuffer('buf-1')
    })

    expect(result.current.state.errorMessage).toBe(
      'Состав корзины изменился, отсканируйте буфер ещё раз'
    )
    expect(result.current.state.phase).toBe('buffer')
    expect(result.current.state.lastBufferResult).toBeNull()
  })
})
