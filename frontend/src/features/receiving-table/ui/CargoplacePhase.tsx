import { Alert, AlertDescription, ScanScene } from '@/shared/ui'

import { cargoplaceUuidSchema } from '../model/schema'
import { ScanForm } from './ScanForm'

interface CargoplacePhaseProps {
  onScan: (value: string) => Promise<void> | void
  isScanning: boolean
  isBlocked: boolean
  successMessage: string | null
}

export function CargoplacePhase({
  onScan,
  isScanning,
  isBlocked,
  successMessage,
}: CargoplacePhaseProps) {
  return (
    <ScanScene
      title="Приёмка на столах"
      description="Отсканируйте грузоместо, чтобы разобрать его на рабочем столе и сверить товары по SKU."
      banner={
        successMessage ? (
          <Alert variant="success">
            <AlertDescription className="col-span-2">
              {successMessage}
            </AlertDescription>
          </Alert>
        ) : null
      }
    >
      <ScanForm
        title="Отсканируйте штрих-код грузоместа"
        inputId="cargoplace-id"
        srLabel="Идентификатор грузоместа"
        placeholder="Данные со штрих-кода"
        submitLabel="Продолжить"
        onScan={onScan}
        isScanning={isScanning}
        isBlocked={isBlocked}
        schema={cargoplaceUuidSchema}
      />
    </ScanScene>
  )
}
