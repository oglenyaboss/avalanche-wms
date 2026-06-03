import { useState } from 'react'

import { Card, CardContent } from '@/shared/ui'

import { useRecentProducts } from '../model/useTraceability'

interface TraceLookupProps {
  onSelect: (key: string) => void
}

// Entry screen: a scan/enter field plus a list of recent products. On stage the
// operator can scan, paste an id, or just click a recent item.
export function TraceLookup({ onSelect }: TraceLookupProps) {
  const [value, setValue] = useState('')
  const { data: recent, isPending, isError } = useRecentProducts()

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = value.trim()
    if (trimmed) onSelect(trimmed)
  }

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-6">
      <header className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold tracking-tight">Трассировка товара</h1>
        <p className="text-muted-foreground">
          Отсканируйте QR, введите идентификатор или выберите товар из списка.
        </p>
      </header>

      <form onSubmit={submit} className="flex gap-2">
        <input
          autoFocus
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="QR-код или product_id"
          aria-label="QR-код или идентификатор товара"
          className="flex-1 rounded-xl border border-border bg-card px-4 py-3 text-sm shadow-soft outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
        <button
          type="submit"
          className="rounded-xl bg-primary px-5 py-3 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
        >
          Найти
        </button>
      </form>

      <section className="flex flex-col gap-3">
        <h2 className="text-xs font-medium tracking-[0.14em] text-muted-foreground uppercase">
          Последние товары
        </h2>
        {isPending ? (
          <p className="text-sm text-muted-foreground">Загрузка…</p>
        ) : isError ? (
          <p className="text-sm text-status-failed">Не удалось загрузить список.</p>
        ) : recent && recent.length > 0 ? (
          <ul className="flex flex-col gap-2">
            {recent.map((p) => (
              <li key={p.productId}>
                <Card size="sm" className="ring-foreground/10">
                  <CardContent className="p-0">
                    <button
                      type="button"
                      onClick={() => onSelect(p.productId)}
                      className="flex w-full items-center justify-between gap-4 px-4 py-3 text-left transition-colors hover:bg-accent/50"
                    >
                      <span className="flex flex-col gap-0.5">
                        <span className="font-mono text-sm font-medium">{p.qrCode}</span>
                        <span className="text-xs text-muted-foreground">{p.skuName}</span>
                      </span>
                      <span className="text-xs text-muted-foreground">{p.status}</span>
                    </button>
                  </CardContent>
                </Card>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">Список пуст.</p>
        )}
      </section>
    </div>
  )
}
