import { Button } from '@/shared/ui'

import type { ActiveShipment } from '../model/gateReducer'
import { CargoplaceProgress } from './CargoplaceProgress'
import { ScanCargoplaceForm } from './ScanCargoplaceForm'

interface ShipmentReceivingProps {
  shipment: ActiveShipment
  onScanCargoplace: (code: string) => Promise<void> | void
  onAcceptShipment: () => void
  isScanning: boolean
  isErrorOpen: boolean
}

export function ShipmentReceiving({
  shipment,
  onScanCargoplace,
  onAcceptShipment,
  isScanning,
  isErrorOpen,
}: ShipmentReceivingProps) {
  return (
    <section className="mx-auto flex max-w-2xl flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="font-heading text-2xl font-semibold">
          Поставка {shipment.ttnCode}
        </h2>
        <Button variant="outline" type="button" onClick={onAcceptShipment}>
          Принять поставку
        </Button>
      </div>

      <div className="flex flex-col gap-3">
        <h3 className="font-medium">Отсканируйте штрих-код грузоместа</h3>
        <ScanCargoplaceForm
          onScan={onScanCargoplace}
          isScanning={isScanning}
          isBlocked={isErrorOpen}
        />
      </div>

      <CargoplaceProgress
        cargoplaces={shipment.cargoplaces}
        progress={shipment.progress}
      />
    </section>
  )
}
