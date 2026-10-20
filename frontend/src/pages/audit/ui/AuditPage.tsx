// Auditor page: DB record from the backend, chain read directly from an RPC node.
import { isAxiosError } from 'axios'
import { useState } from 'react'
import { useSearchParams } from 'react-router'
import { isAddress } from 'viem'

import { AuditSettings, AuditStages, useAuditSettings, useProductAudit } from '@/features/audit'
import { Input } from '@/shared/ui'

// Axios rejects with an AxiosError on non-2xx (before the unwrap runs), so
// error.message is the generic "Request failed…". Pull the backend envelope
// message instead — an AxiosError IS an Error, so an `instanceof Error` check
// would shadow the friendly copy.
function auditErrorText(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error)
  if (isAxiosError(error)) {
    if (error.response?.status === 404) return 'Товар не найден.'
    const backend = (error.response?.data as { error?: { message?: string } } | undefined)?.error?.message
    return `Не удалось выполнить сверку: ${backend ?? message}`
  }
  return `Не удалось выполнить сверку: ${message}`
}

export function AuditPage() {
  const [params, setParams] = useSearchParams()
  const key = params.get('key')
  const { rpcUrl, contractAddress } = useAuditSettings()
  const settingsReady = rpcUrl !== '' && isAddress(contractAddress)
  const { data, isPending, isError, error } = useProductAudit(key, rpcUrl, contractAddress)
  const [lookup, setLookup] = useState('')

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = lookup.trim()
    if (trimmed) setParams({ key: trimmed })
  }

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6 px-6 py-10">
      <header className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold tracking-tight">Аудит товара</h1>
        <p className="text-muted-foreground">
          Сверка подписанных действий: детали — из БД WMS, цепь читается напрямую с RPC-узла.
        </p>
      </header>

      <AuditSettings />

      {!settingsReady ? (
        <p className="text-sm text-status-failed">Укажите RPC-узел и адрес контракта</p>
      ) : null}

      {!key ? (
        <form onSubmit={submit} className="flex gap-2">
          <Input
            autoFocus
            value={lookup}
            onChange={(e) => setLookup(e.target.value)}
            placeholder="QR-код или product_id"
            aria-label="QR-код или идентификатор товара"
          />
          <button
            type="submit"
            className="rounded-xl bg-primary px-5 py-3 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
          >
            Проверить
          </button>
        </form>
      ) : settingsReady ? (
        <div className="flex flex-col gap-4">
          <button
            type="button"
            onClick={() => setParams({})}
            className="self-start text-sm text-muted-foreground hover:text-foreground"
          >
            ← К поиску
          </button>
          {isPending ? (
            <p className="text-sm text-muted-foreground">Сверка…</p>
          ) : isError ? (
            <p className="text-sm text-status-failed">{auditErrorText(error)}</p>
          ) : data ? (
            <div className="flex flex-col gap-4">
              <p className="font-mono text-sm text-muted-foreground">Товар: {data.product.productId}</p>
              {data.stages.length === 0 ? (
                <p className="text-sm text-muted-foreground">Нет подписанных действий по товару</p>
              ) : (
                <AuditStages stages={data.stages} />
              )}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
