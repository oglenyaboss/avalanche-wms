import { useState } from 'react'

import {
  Card,
  CardContent,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/shared/ui'

import type { ProductTimeline as Timeline } from '../model/types'
import { TimelineStep } from './TimelineStep'
import { TxProofPanel } from './TxProofPanel'

interface ProductTimelineProps {
  timeline: Timeline
}

export function ProductTimeline({ timeline }: ProductTimelineProps) {
  const [selectedHash, setSelectedHash] = useState<string | null>(null)
  const { product, steps } = timeline

  return (
    <div className="flex flex-col gap-5">
      <header className="flex flex-col gap-1">
        <span className="text-xs font-medium tracking-[0.14em] text-muted-foreground uppercase">
          {product.skuName}
        </span>
        <h2 className="font-mono text-2xl font-semibold tracking-tight text-foreground">
          {product.qrCode}
        </h2>
        <span className="text-sm text-muted-foreground">Статус: {product.status}</span>
      </header>

      <Card>
        <CardContent className="pt-6">
          {steps.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              По этому товару ещё нет событий — он не прошёл ни одной операции.
            </p>
          ) : (
            <ol className="flex flex-col">
              {steps.map((step, i) => (
                <TimelineStep
                  key={step.eventId}
                  step={step}
                  isLast={i === steps.length - 1}
                  onSelectHash={setSelectedHash}
                />
              ))}
            </ol>
          )}
        </CardContent>
      </Card>

      <Sheet open={selectedHash !== null} onOpenChange={(o) => !o && setSelectedHash(null)}>
        <SheetContent side="right" className="w-full max-w-md p-6">
          <SheetHeader className="mb-4 p-0">
            <SheetTitle>On-chain проверка</SheetTitle>
            <SheetDescription className="sr-only">
              Проверка транзакции товара в сети Avalanche
            </SheetDescription>
          </SheetHeader>
          {selectedHash ? <TxProofPanel hash={selectedHash} /> : null}
        </SheetContent>
      </Sheet>
    </div>
  )
}
