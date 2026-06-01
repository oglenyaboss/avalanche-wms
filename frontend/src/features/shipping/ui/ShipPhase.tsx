import type {
  DispatchInfo,
  OutboundDispatchStatus,
  ShippingBufferBin,
  ShippingBufferProduct,
} from '@/entities/shipping'
import {
  Alert,
  AlertDescription,
  AlertTitle,
  Button,
  Card,
  CardContent,
} from '@/shared/ui'
import { cn } from '@/shared/lib'

import { CheckIcon, TruckIcon, WarningIcon } from './icons'
import { productQrSchema } from '../model/schema'
import { ScanForm } from './ScanForm'

interface ShipPhaseProps {
  bufferBin: ShippingBufferBin
  products: ShippingBufferProduct[]
  dispatch: DispatchInfo
  selectedProductIds: string[]
  destinationMismatch: boolean
  partialMessage: string | null
  onToggleProduct: (productId: string) => void
  onScanProduct: (value: string) => Promise<void> | void
  onClearSelection: () => void
  onShipBulk: () => void
  onShipSpot: () => void
  onBack: () => void
  isShipping: boolean
  isBlocked: boolean
}

const DISPATCH_STATUS_LABEL: Record<OutboundDispatchStatus, string> = {
  SCHEDULED: 'Запланирован',
  AT_GATE: 'У ворот',
  DEPARTED: 'Отбыл',
  CANCELLED: 'Отменён',
}

// Step 3 of shipping: the truck is at the gate and the buffer is open. Ship the
// whole buffer (bulk) or scan/select specific items (spot). A destination
// mismatch between the buffer's zone and the truck's dispatch blocks shipping.
export function ShipPhase({
  bufferBin,
  products,
  dispatch,
  selectedProductIds,
  destinationMismatch,
  partialMessage,
  onToggleProduct,
  onScanProduct,
  onClearSelection,
  onShipBulk,
  onShipSpot,
  onBack,
  isShipping,
  isBlocked,
}: ShipPhaseProps) {
  const selectedCount = selectedProductIds.length
  const selectedSet = new Set(selectedProductIds)
  const shipDisabled = destinationMismatch || isShipping

  return (
    <section className="mx-auto flex max-w-2xl flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <h2 className="text-lg font-semibold">
            Отгрузка · {bufferBin.destination.name}
          </h2>
          <p className="text-sm text-muted-foreground">
            Ячейка {bufferBin.code}
          </p>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onBack}
          disabled={isShipping}
        >
          ← Отсканировать другого водителя
        </Button>
      </div>

      {/* Arrived truck. */}
      <Card size="sm" className="ring-foreground/10">
        <CardContent className="flex items-start gap-4 px-4 py-3">
          <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-accent text-accent-foreground">
            <TruckIcon className="size-5" />
          </span>
          <span className="flex flex-1 flex-col gap-0.5">
            <span className="text-lg font-semibold tabular-nums">
              {dispatch.vehicleNumber}
            </span>
            <span className="text-sm">
              {dispatch.driverName}
              {dispatch.driverPhone ? ` · ${dispatch.driverPhone}` : ''}
            </span>
            <span className="text-xs text-muted-foreground">
              Рейс {dispatch.dispatchCode} ·{' '}
              {DISPATCH_STATUS_LABEL[dispatch.status]}
            </span>
          </span>
        </CardContent>
      </Card>

      {/* Destination match — the core safeguard against loading the wrong truck. */}
      {destinationMismatch ? (
        <Alert variant="destructive">
          <WarningIcon className="size-5" />
          <div className="col-start-2 flex flex-col gap-1">
            <AlertTitle>Машина на другую зону</AlertTitle>
            <AlertDescription>
              Рейс назначен на «{dispatch.destination.name}», а буфер — «
              {bufferBin.destination.name}». Разверните машину или отсканируйте
              нужный буфер. Отгрузка заблокирована.
            </AlertDescription>
          </div>
        </Alert>
      ) : (
        <Alert variant="success">
          <CheckIcon className="size-5" />
          <AlertDescription className="col-start-2 self-center">
            Зоны совпадают: {bufferBin.destination.name}.
          </AlertDescription>
        </Alert>
      )}

      {partialMessage ? (
        <Alert variant="success">
          <AlertDescription>{partialMessage}</AlertDescription>
        </Alert>
      ) : null}

      {/* Buffer contents + spot selection. */}
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="font-medium">Товары в буфере ({products.length})</h3>
          {selectedCount > 0 ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onClearSelection}
            >
              Снять выделение ({selectedCount})
            </Button>
          ) : null}
        </div>

        <ScanForm
          title="Точечная отгрузка: отсканируйте QR загружаемого товара"
          inputId="shipping-spot"
          srLabel="QR-код товара"
          placeholder="Данные с QR-кода"
          submitLabel="Выбрать"
          manualHint="Или отметьте товары в списке ниже"
          onScan={onScanProduct}
          isScanning={isShipping}
          isBlocked={isBlocked}
          schema={productQrSchema}
        />

        {products.length > 0 ? (
          <ul className="flex max-h-80 flex-col gap-2 overflow-y-auto">
            {products.map((product) => {
              const selected = selectedSet.has(product.productId)
              return (
                <li key={product.productId}>
                  <button
                    type="button"
                    aria-pressed={selected}
                    onClick={() => onToggleProduct(product.productId)}
                    className={cn(
                      'flex w-full items-center gap-3 rounded-md border px-4 py-3 text-left transition-colors',
                      'hover:bg-accent/50 focus-visible:bg-accent/50 focus-visible:outline-none',
                      selected
                        ? 'border-foreground/30 bg-accent/40'
                        : 'border-border',
                    )}
                  >
                    <span
                      aria-hidden="true"
                      className={cn(
                        'flex size-5 shrink-0 items-center justify-center rounded border',
                        selected
                          ? 'border-foreground bg-foreground text-background'
                          : 'border-input',
                      )}
                    >
                      {selected ? <CheckIcon className="size-3.5" /> : null}
                    </span>
                    <span className="flex flex-1 flex-col">
                      <span className="font-medium">{product.skuName}</span>
                      <span className="text-xs text-muted-foreground">
                        {product.qrCode} ·{' '}
                        {product.orderExternalNo
                          ? `заказ ${product.orderExternalNo}`
                          : 'без заказа'}
                      </span>
                    </span>
                  </button>
                </li>
              )
            })}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">
            В буфере нет товаров, готовых к отгрузке.
          </p>
        )}
      </div>

      {/* Actions: bulk is the primary path, spot the точечный fallback. */}
      <div className="flex flex-wrap gap-3">
        <Button
          type="button"
          onClick={onShipBulk}
          disabled={shipDisabled || products.length === 0}
        >
          {isShipping ? 'Отгрузка...' : `Отгрузить весь буфер (${products.length})`}
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={onShipSpot}
          disabled={shipDisabled || selectedCount === 0}
        >
          Отгрузить выбранные ({selectedCount})
        </Button>
      </div>
    </section>
  )
}
