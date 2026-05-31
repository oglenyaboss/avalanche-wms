import type { ExpectedSku } from '@/entities/cargoplace'

interface SkuManifestProps {
  skus: ExpectedSku[]
  totalExpected: number
}

// The expected SKU manifest returned by scan-cargoplace: what should be inside.
export function SkuManifest({ skus, totalExpected }: SkuManifestProps) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="font-medium">Ожидается к приёмке</h3>
      <p className="text-sm text-muted-foreground">
        Всего единиц: {totalExpected}
      </p>
      {skus.length > 0 ? (
        <ul className="flex flex-col divide-y divide-border rounded-md border border-border">
          {skus.map((sku) => (
            <li
              key={sku.skuId}
              className="flex items-center justify-between gap-4 px-4 py-2"
            >
              <span className="font-medium">{sku.skuName}</span>
              <span className="text-sm text-muted-foreground tabular-nums">
                {sku.expectedQty} шт.
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted-foreground">Манифест пуст.</p>
      )}
    </section>
  )
}
