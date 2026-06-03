import { useTxProof } from '../model/useTraceability'

interface TxProofPanelProps {
  hash: string
}

const STATUS_TEXT: Record<string, string> = {
  success: '✓ Подтверждена в сети',
  failed: '✕ Транзакция отклонена',
  pending: '• Ещё не подтверждена (в мемпуле)',
}

// Live RPC verification of one tx, fetched on demand. Renders inside a Sheet
// opened by ProductTimeline. Independent proof: this hits the chain, not our DB.
export function TxProofPanel({ hash }: TxProofPanelProps) {
  const { data, isPending, isError, refetch, isFetching } = useTxProof(hash)

  return (
    <div className="flex flex-col gap-4">
      <div>
        <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          Хеш транзакции
        </p>
        <p className="font-mono text-xs break-all text-foreground">{hash}</p>
      </div>

      {isPending ? (
        <p className="text-sm text-muted-foreground">Проверяем в сети…</p>
      ) : isError ? (
        <div className="flex flex-col gap-3">
          <p className="text-sm text-status-failed">
            Сеть недоступна. Попробуйте ещё раз.
          </p>
          <button
            type="button"
            onClick={() => refetch()}
            className="self-start rounded-lg border border-border px-3 py-1.5 text-sm font-medium transition-colors hover:bg-accent"
          >
            Проверить ещё раз
          </button>
        </div>
      ) : data ? (
        <dl className="flex flex-col gap-3">
          <Row label="Статус" value={STATUS_TEXT[data.status] ?? data.status} />
          {data.found ? (
            <>
              <Row label="Блок" value={`#${data.blockNumber.toLocaleString('ru-RU')}`} />
              <Row label="Подтверждений" value={String(data.confirmations)} />
              <Row label="Gas" value={data.gasUsed.toLocaleString('ru-RU')} />
              <Row label="Контракт" value="BatchMappingWMS · Avalanche Subnet" />
            </>
          ) : null}
          <button
            type="button"
            onClick={() => refetch()}
            disabled={isFetching}
            className="mt-2 self-start rounded-lg border border-border px-3 py-1.5 text-sm font-medium transition-colors hover:bg-accent disabled:opacity-50"
          >
            {isFetching ? 'Проверяем…' : 'Проверить ещё раз'}
          </button>
        </dl>
      ) : null}
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border pb-2">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium text-foreground tabular-nums">{value}</dd>
    </div>
  )
}
