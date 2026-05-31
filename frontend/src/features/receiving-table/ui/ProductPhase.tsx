import type { ResolvedSku, ScannedProduct } from '@/entities/cargoplace'
import { Alert, AlertDescription, Button } from '@/shared/ui'

import { scanCodeSchema } from '../model/schema'
import type {
  ActiveBox,
  ActiveCargoplace,
  ProductStep,
} from '../model/tableReducer'
import { ContextSummary } from './ContextSummary'
import { ScannedProducts } from './ScannedProducts'
import { ScanForm } from './ScanForm'

interface ProductPhaseProps {
  cargoplace: ActiveCargoplace
  box: ActiveBox
  productStep: ProductStep
  resolvedSku: ResolvedSku | null
  scannedProducts: ScannedProduct[]
  onScanBarcode: (value: string) => Promise<void> | void
  onScanQr: (value: string) => Promise<void> | void
  onCloseBox: () => void
  isScanningBarcode: boolean
  isScanningQr: boolean
  isBlocked: boolean
}

export function ProductPhase({
  cargoplace,
  box,
  productStep,
  resolvedSku,
  scannedProducts,
  onScanBarcode,
  onScanQr,
  onCloseBox,
  isScanningBarcode,
  isScanningQr,
  isBlocked,
}: ProductPhaseProps) {
  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <ContextSummary
          cargoplaceCode={cargoplace.cargoplaceCode}
          boxBarcode={box.boxBarcode}
          productBarcode={productStep === 'qr' ? resolvedSku?.barcode : null}
        />
        <Button variant="outline" type="button" onClick={onCloseBox}>
          Принять коробку
        </Button>
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        {/* Remount between sub-steps (key) so the input resets and refocuses. */}
        {productStep === 'barcode' ? (
          <ScanForm
            key="barcode"
            title="Отсканируйте штрих-код товара"
            inputId="product-barcode"
            srLabel="Штрих-код товара"
            placeholder="Данные со штрих-кода"
            submitLabel="Далее"
            onScan={onScanBarcode}
            isScanning={isScanningBarcode}
            isBlocked={isBlocked}
            schema={scanCodeSchema}
          />
        ) : (
          <ScanForm
            key="qr"
            title="Отсканируйте QR-код, который вы приклеили на текущий товар"
            inputId="product-qr"
            srLabel="QR-код товара"
            placeholder="Данные с QR-кода"
            submitLabel="Далее"
            manualHint="В случае неполадки сканирования, введите QR вручную"
            helper={
              resolvedSku ? (
                <Alert variant="success">
                  <AlertDescription className="col-span-2">
                    {resolvedSku.skuName}: {resolvedSku.message}
                  </AlertDescription>
                </Alert>
              ) : null
            }
            onScan={onScanQr}
            isScanning={isScanningQr}
            isBlocked={isBlocked}
            schema={scanCodeSchema}
          />
        )}

        <ScannedProducts
          products={scannedProducts}
          progress={cargoplace.progress}
        />
      </div>
    </section>
  )
}
