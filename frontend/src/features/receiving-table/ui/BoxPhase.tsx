import type { ScannedProduct } from '@/entities/cargoplace'
import { Button } from '@/shared/ui'

import { scanCodeSchema } from '../model/schema'
import type { ActiveCargoplace } from '../model/tableReducer'
import { ContextSummary } from './ContextSummary'
import { ScannedProducts } from './ScannedProducts'
import { ScanForm } from './ScanForm'
import { SkuManifest } from './SkuManifest'

interface BoxPhaseProps {
  cargoplace: ActiveCargoplace
  scannedProducts: ScannedProduct[]
  onScanBox: (value: string) => Promise<void> | void
  onAcceptCargoplace: () => void
  isScanning: boolean
  isBlocked: boolean
}

export function BoxPhase({
  cargoplace,
  scannedProducts,
  onScanBox,
  onAcceptCargoplace,
  isScanning,
  isBlocked,
}: BoxPhaseProps) {
  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <ContextSummary cargoplaceCode={cargoplace.cargoplaceCode} />
        {/* Disabled while a box scan is in flight: a late BOX_OPENED response
            would otherwise pull the operator into the product phase after they
            confirmed the move to buffer. */}
        <Button
          variant="outline"
          type="button"
          onClick={onAcceptCargoplace}
          disabled={isScanning}
        >
          Принять грузоместо
        </Button>
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        <ScanForm
          title="Отсканируйте штрих-код коробки"
          inputId="box-barcode"
          srLabel="Штрих-код коробки"
          placeholder="Данные со штрих-кода"
          submitLabel="Перейти к коробке"
          onScan={onScanBox}
          isScanning={isScanning}
          isBlocked={isBlocked}
          schema={scanCodeSchema}
        />

        <div className="flex flex-col gap-6">
          <SkuManifest
            skus={cargoplace.expectedSkus}
            totalExpected={cargoplace.totalExpected}
          />
          <ScannedProducts
            products={scannedProducts}
            progress={cargoplace.progress}
          />
        </div>
      </div>
    </section>
  )
}
