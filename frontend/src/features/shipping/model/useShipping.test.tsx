// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { DispatchInfo, ScanBufferResult, ShipResult } from '@/entities/shipping'
import type { PartAssertion, SignedOperationDraft } from '@/entities/signed-action'

const apiMocks = vi.hoisted(() => ({
  draftShip: vi.fn(),
  scanBuffer: vi.fn(),
  scanDriver: vi.fn(),
  submitShip: vi.fn(),
}))

vi.mock('../api/shippingApi', () => apiMocks)

const signMocks = vi.hoisted(() => ({
  signOperation: vi.fn(),
}))

vi.mock('@/entities/signed-action', () => signMocks)

import { useShipping } from './useShipping'

const BUFFER: ScanBufferResult = {
  bufferBin: {
    id: 'buf-1',
    code: 'BIN-SHIP-1',
    destination: { id: 'dest-1', code: 'SHOP-1', name: 'Магазин №1' },
  },
  products: [
    {
      productId: 'p-1',
      qrCode: 'QR-1',
      skuName: 'Кроссовки',
      orderExternalNo: 'ORD-100',
    },
    {
      productId: 'p-2',
      qrCode: 'QR-2',
      skuName: 'Футболка',
      orderExternalNo: null,
    },
  ],
  count: 2,
}

const DISPATCH: DispatchInfo = {
  dispatchId: 'dsp-1',
  dispatchCode: 'DSP-2026-0421-001',
  vehicleNumber: 'A123BC777',
  driverName: 'Иван Петров',
  driverPhone: null,
  destination: { id: 'dest-1', code: 'SHOP-1', name: 'Магазин №1' },
  status: 'AT_GATE',
  arrivedAt: '2026-06-01T09:55:00Z',
}

// A two-part draft: the operator is asked once per part.
const DRAFT: SignedOperationDraft = {
  operationId: 'op-1',
  actionType: 6,
  signerKeyId: '0xabc',
  rpId: 'localhost',
  allowCredentials: [{ type: 'public-key', id: 'AQID' }],
  userVerification: 'required',
  timeout: 60000,
  parts: [
    { actionId: 'a-1', itemIds: ['p-1'], detailsHash: '0x1', challenge: 'AQID' },
    { actionId: 'a-2', itemIds: ['p-2'], detailsHash: '0x2', challenge: 'AQID' },
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
  {
    actionId: 'a-2',
    credentialId: 'BQ',
    authenticatorData: 'Bg',
    clientDataJson: 'Bw',
    signature: 'CA',
  },
]

const SHIPPED: ShipResult = {
  productsShipped: 2,
  outboxEventsCreated: 2,
  ordersCompleted: 1,
  ordersPartiallyShipped: 0,
  dispatchDeparted: true,
  bufferRemaining: 0,
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

function renderShipping() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return renderHook(() => useShipping(), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
}

type UseShippingResult = ReturnType<typeof useShipping>

// Walk the flow to the ship phase: scan the buffer, then the driver.
async function reachShipPhase(result: { current: UseShippingResult }) {
  await act(async () => {
    await result.current.submitBufferScan('buf-1')
  })
  await act(async () => {
    await result.current.submitDriverScan('DSP-2026-0421-001')
  })
  await waitFor(() => expect(result.current.state.phase).toBe('ship'))
}

afterEach(() => {
  vi.resetAllMocks()
})

describe('useShipping performShip', () => {
  it('drafts, signs per part and submits, then finishes the shipment', async () => {
    apiMocks.scanBuffer.mockResolvedValue(BUFFER)
    apiMocks.scanDriver.mockResolvedValue(DISPATCH)
    apiMocks.draftShip.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockResolvedValue(ASSERTIONS)
    apiMocks.submitShip.mockResolvedValue(SHIPPED)

    const { result } = renderShipping()
    await reachShipPhase(result)

    await act(async () => {
      await result.current.shipBulk()
    })

    // Bulk mode: no productIds. The two-part draft produced two assertions.
    expect(apiMocks.draftShip).toHaveBeenCalledWith({
      bufferBinId: 'buf-1',
      dispatchId: 'dsp-1',
      productIds: undefined,
    })
    expect(signMocks.signOperation).toHaveBeenCalledWith(DRAFT)
    expect(apiMocks.submitShip).toHaveBeenCalledWith('op-1', ASSERTIONS)
    // The dispatch departed → terminal done phase with the ship result.
    expect(result.current.state.phase).toBe('done')
    expect(result.current.state.lastShipResult).toEqual(SHIPPED)
    expect(result.current.state.errorMessage).toBeNull()
  })

  it('shows the signing error text when the authenticator cancels the prompt', async () => {
    apiMocks.scanBuffer.mockResolvedValue(BUFFER)
    apiMocks.scanDriver.mockResolvedValue(DISPATCH)
    apiMocks.draftShip.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockRejectedValue(
      new DOMException('denied', 'NotAllowedError'),
    )

    const { result } = renderShipping()
    await reachShipPhase(result)

    await act(async () => {
      await result.current.shipBulk()
    })

    expect(result.current.state.errorMessage).toBe(
      'Подтверждение отменено или истекло время ожидания',
    )
    expect(apiMocks.submitShip).not.toHaveBeenCalled()
    expect(result.current.state.phase).toBe('ship')
  })

  it('surfaces the PICKING_NOT_CONFIRMED message', async () => {
    apiMocks.scanBuffer.mockResolvedValue(BUFFER)
    apiMocks.scanDriver.mockResolvedValue(DISPATCH)
    apiMocks.draftShip.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockResolvedValue(ASSERTIONS)
    apiMocks.submitShip.mockRejectedValue(
      axiosError(
        409,
        envelope(
          'PICKING_NOT_CONFIRMED',
          'Сборка части товаров ещё не подтверждена в цепи. Повторите отгрузку чуть позже',
        ),
      ),
    )

    const { result } = renderShipping()
    await reachShipPhase(result)

    await act(async () => {
      await result.current.shipBulk()
    })

    expect(result.current.state.errorMessage).toBe(
      'Сборка части товаров ещё не подтверждена в цепи. Повторите отгрузку чуть позже',
    )
    expect(result.current.state.phase).toBe('ship')
    expect(result.current.state.lastShipResult).toBeNull()
  })

  it('reloads the buffer after a partial ship', async () => {
    apiMocks.scanBuffer.mockResolvedValue(BUFFER)
    apiMocks.scanDriver.mockResolvedValue(DISPATCH)
    apiMocks.draftShip.mockResolvedValue(DRAFT)
    signMocks.signOperation.mockResolvedValue(ASSERTIONS)
    apiMocks.submitShip.mockResolvedValue({
      ...SHIPPED,
      productsShipped: 1,
      dispatchDeparted: false,
      bufferRemaining: 1,
    })
    // The post-partial reload returns the remaining buffer.
    apiMocks.scanBuffer.mockResolvedValueOnce(BUFFER).mockResolvedValueOnce({
      ...BUFFER,
      products: [BUFFER.products[1]],
      count: 1,
    })

    const { result } = renderShipping()
    await reachShipPhase(result)

    await act(async () => {
      await result.current.shipBulk()
    })

    expect(result.current.state.phase).toBe('ship')
    expect(result.current.state.partialMessage).toContain('Осталось в буфере: 1')
    expect(result.current.state.products).toEqual([BUFFER.products[1]])
    expect(result.current.state.lastShipResult?.operationId).toBe('op-1')
  })
})
