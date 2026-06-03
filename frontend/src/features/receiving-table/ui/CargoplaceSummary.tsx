import type { CloseCargoplaceSummary } from '@/entities/cargoplace'
import { Alert, AlertDescription } from '@/shared/ui'

interface CargoplaceSummaryProps {
  summary: CloseCargoplaceSummary
}

// The close-cargoplace result: received vs expected, plus the per-SKU shortage
// breakdown (overages and damage are not modeled by the backend).
export function CargoplaceSummary({ summary }: CargoplaceSummaryProps) {
  const hasShortage = summary.shortage > 0

  return (
    <section className="flex flex-col gap-3">
      <Alert variant={hasShortage ? 'default' : 'success'}>
        <AlertDescription className="col-span-2">
          Принято {summary.productsReceived} из {summary.productsExpected}
          {hasShortage ? `, недостача ${summary.shortage}` : ''}.
        </AlertDescription>
      </Alert>

      {hasShortage && summary.shortageBySku.length > 0 ? (
        <div className="flex flex-col gap-2">
          <h4 className="font-medium">Недостача по SKU</h4>
          <ul className="flex flex-col divide-y divide-border rounded-md border border-border">
            {/* The summary is static once the cargoplace is closed and never
                reordered, and ShortageBySku carries no stable id, so the index
                is an honest key here. */}
            {summary.shortageBySku.map((row, index) => (
              <li
                key={index}
                className="flex items-center justify-between gap-4 px-4 py-2"
              >
                <span className="font-medium">{row.skuName}</span>
                <span className="text-sm text-destructive tabular-nums">
                  −{row.shortage} (принято {row.received} из {row.expected})
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  )
}
