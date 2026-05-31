import type { ReceivingProgress, ScannedProduct } from '@/entities/cargoplace'

interface ScannedProductsProps {
  products: ScannedProduct[]
  progress: ReceivingProgress
}

// The running list of products registered for the current cargoplace, with the
// cargoplace-level received/expected progress.
export function ScannedProducts({ products, progress }: ScannedProductsProps) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="font-medium">Принятые товары</h3>
      <p className="text-sm font-medium text-muted-foreground">
        Принято {progress.received} из {progress.expected}
      </p>
      {products.length > 0 ? (
        <ul className="flex max-h-72 flex-col divide-y divide-border overflow-y-auto rounded-md border border-border">
          {products.map((product, index) => (
            <li
              key={product.productId}
              className="flex items-center justify-between gap-4 px-4 py-2"
            >
              <span className="flex items-center gap-2">
                <span className="text-xs tabular-nums text-muted-foreground">
                  {index + 1}
                </span>
                <span className="font-medium">{product.skuName}</span>
              </span>
              <span className="max-w-[12rem] truncate text-sm text-muted-foreground">
                {product.qrCode}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted-foreground">
          Пока ничего не отсканировано.
        </p>
      )}
    </section>
  )
}
