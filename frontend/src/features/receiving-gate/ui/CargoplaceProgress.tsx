import type { Cargoplace, GateProgress } from '@/entities/shipment'
import {
  getCargoplaceStatusLabel,
  getCargoplaceStatusTone,
} from '@/entities/shipment'

const toneClassName: Record<
  ReturnType<typeof getCargoplaceStatusTone>,
  string
> = {
  pending: 'text-muted-foreground',
  received: 'text-emerald-600',
  missing: 'text-destructive',
  neutral: 'text-foreground',
}

interface CargoplaceProgressProps {
  cargoplaces: Cargoplace[]
  progress: GateProgress
}

export function CargoplaceProgress({
  cargoplaces,
  progress,
}: CargoplaceProgressProps) {
  return (
    <section className="flex flex-col gap-3">
      <p className="text-sm font-medium text-muted-foreground">
        Принято {progress.received} из {progress.total}
      </p>
      {cargoplaces.length > 0 ? (
        <ul className="flex flex-col divide-y divide-border rounded-md border border-border">
          {cargoplaces.map((cargoplace) => (
            <li
              key={cargoplace.id}
              className="flex items-center justify-between gap-4 px-4 py-3"
            >
              <span className="font-medium">{cargoplace.code}</span>
              <span
                className={`text-sm ${toneClassName[getCargoplaceStatusTone(cargoplace.status)]}`}
              >
                {getCargoplaceStatusLabel(cargoplace.status)}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  )
}
