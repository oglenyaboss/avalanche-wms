import { useState } from 'react'

import type { PickedProduct } from '@/entities/putaway'
import {
  Button,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/shared/ui'

const PAGE_SIZE = 20

interface DrafterSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  cart: PickedProduct[]
  placedProductIds: string[]
}

// The "Товары на раскладку" side drawer: the running list of products picked
// into the putaway task, paginated 20 per page to match the Figma.
export function DrafterSheet({
  open,
  onOpenChange,
  cart,
  placedProductIds,
}: DrafterSheetProps) {
  const [page, setPage] = useState(0)
  const placed = new Set(placedProductIds)

  const pageCount = Math.max(1, Math.ceil(cart.length / PAGE_SIZE))
  // Clamp the page if the cart shrank (or the drawer reopened on a stale page).
  const safePage = Math.min(page, pageCount - 1)
  const start = safePage * PAGE_SIZE
  const visible = cart.slice(start, start + PAGE_SIZE)

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="gap-0">
        <SheetHeader>
          <SheetTitle>Товары на раскладку</SheetTitle>
          <SheetDescription>
            SKU ранее отсканированных товаров, добавленных в задачу раскладки
          </SheetDescription>
        </SheetHeader>

        {cart.length > 0 ? (
          <ol className="flex flex-1 flex-col divide-y divide-border overflow-y-auto border-t border-border">
            {visible.map((product, index) => (
              <li
                key={product.productId}
                className="flex items-center justify-between gap-4 px-6 py-2.5"
              >
                <span className="flex items-center gap-3">
                  <span className="w-6 text-xs tabular-nums text-muted-foreground">
                    {start + index + 1}
                  </span>
                  <span className="font-medium">{product.skuName}</span>
                </span>
                <span className="flex items-center gap-2">
                  <span className="max-w-[10rem] truncate text-xs text-muted-foreground">
                    {product.qrCode}
                  </span>
                  {placed.has(product.productId) ? (
                    <span className="rounded-sm bg-emerald-100 px-1.5 py-0.5 text-[11px] font-medium text-emerald-700">
                      размещён
                    </span>
                  ) : null}
                </span>
              </li>
            ))}
          </ol>
        ) : (
          <p className="flex-1 border-t border-border px-6 py-6 text-sm text-muted-foreground">
            Пока ничего не добавлено в задачу.
          </p>
        )}

        <div className="flex items-center justify-between gap-4 border-t border-border p-4">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            disabled={safePage === 0}
          >
            ← Назад
          </Button>
          <span className="text-sm tabular-nums text-muted-foreground">
            {safePage + 1} / {pageCount}
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
            disabled={safePage >= pageCount - 1}
          >
            Вперёд →
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  )
}
