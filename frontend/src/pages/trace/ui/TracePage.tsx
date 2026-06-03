import { isAxiosError } from 'axios'
import { useSearchParams } from 'react-router'

import { ProductTimeline, TraceLookup, useProductTimeline } from '@/features/traceability'

// The query rejects with an AxiosError on non-2xx (before unwrap runs), so
// error.message is the generic "Request failed…". Pull the backend envelope
// message instead — an AxiosError IS an Error, so an `instanceof Error` check
// would shadow the friendly copy.
function timelineErrorText(error: unknown): string {
  if (isAxiosError(error)) {
    const msg = (error.response?.data as { error?: { message?: string } } | undefined)?.error
      ?.message
    if (error.response?.status === 404) return msg ?? 'Товар не найден.'
    return msg ?? 'Не удалось загрузить историю.'
  }
  return 'Не удалось загрузить историю.'
}

export function TracePage() {
  const [params, setParams] = useSearchParams()
  const key = params.get('key')

  const { data, isPending, isError, error } = useProductTimeline(key)

  const select = (k: string) => setParams({ key: k })

  return (
    <div className="min-h-[calc(100vh-5rem)] px-6 py-10">
      {!key ? (
        <TraceLookup onSelect={select} />
      ) : (
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-6">
          <button
            type="button"
            onClick={() => setParams({})}
            className="self-start text-sm text-muted-foreground hover:text-foreground"
          >
            ← К поиску
          </button>
          {isPending ? (
            <p className="text-sm text-muted-foreground">Загрузка истории…</p>
          ) : isError ? (
            <p className="text-sm text-status-failed">{timelineErrorText(error)}</p>
          ) : data ? (
            <ProductTimeline timeline={data} />
          ) : null}
        </div>
      )}
    </div>
  )
}
