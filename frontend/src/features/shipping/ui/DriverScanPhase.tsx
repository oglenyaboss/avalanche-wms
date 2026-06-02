import type { ShippingBufferBin } from '@/entities/shipping'
import { Alert, AlertDescription, Button, Card, CardContent } from '@/shared/ui'

import { dispatchCodeSchema } from '../model/schema'
import { ScanForm } from './ScanForm'

interface DriverScanPhaseProps {
  bufferBin: ShippingBufferBin
  productCount: number
  onScan: (value: string) => Promise<void> | void
  onBack: () => void
  isScanning: boolean
  isBlocked: boolean
}

// Step 2 of shipping: with the buffer open, scan the driver's QR (dispatch code)
// to register the truck's arrival at the gate (SCHEDULED → AT_GATE).
export function DriverScanPhase({
  bufferBin,
  productCount,
  onScan,
  onBack,
  isScanning,
  isBlocked,
}: DriverScanPhaseProps) {
  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="self-start"
        onClick={onBack}
        disabled={isScanning}
      >
        ← Отсканировать другой буфер
      </Button>

      <Card size="sm" className="ring-foreground/10">
        <CardContent className="flex items-center justify-between gap-4 px-4 py-3">
          <span className="flex flex-col gap-0.5">
            <span className="text-sm text-muted-foreground">Буфер отгрузки</span>
            <span className="font-semibold">{bufferBin.destination.name}</span>
            <span className="text-sm text-muted-foreground">
              Ячейка {bufferBin.code}
            </span>
          </span>
          <span className="shrink-0 text-right">
            <span className="block text-2xl font-semibold tabular-nums">
              {productCount}
            </span>
            <span className="text-sm text-muted-foreground">
              {productCount === 1 ? 'товар готов' : 'товаров готово'}
            </span>
          </span>
        </CardContent>
      </Card>

      <Alert variant="default">
        <AlertDescription>
          Сверьте магазин на буфере с пунктом назначения приехавшей машины перед
          отгрузкой.
        </AlertDescription>
      </Alert>

      <ScanForm
        title="Отсканируйте QR водителя (код рейса)"
        inputId="shipping-driver"
        srLabel="Код рейса"
        placeholder="Например, DSP-2026-0421-001"
        submitLabel="Зарегистрировать"
        manualHint="В случае неполадки сканирования, введите код рейса вручную"
        onScan={onScan}
        isScanning={isScanning}
        isBlocked={isBlocked}
        schema={dispatchCodeSchema}
      />
    </section>
  )
}
