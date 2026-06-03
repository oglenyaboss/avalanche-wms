import { STATUS_COLOR, formatDateTime, stageLabel, truncateHash } from '@/shared/lib'

import type { TimelineStep as Step } from '../model/types'

interface TimelineStepProps {
  step: Step
  isLast: boolean
  onSelectHash: (hash: string) => void
}

const CHAIN_TEXT: Record<string, string> = {
  committed: 'подтверждено',
  sent: 'отправлено',
  pending: 'ожидает сети',
  failed: 'ошибка',
}

export function TimelineStep({ step, isLast, onSelectHash }: TimelineStepProps) {
  const color = STATUS_COLOR[step.chainStatus]
  return (
    <li className="relative flex gap-4 pb-6">
      {!isLast ? (
        <span aria-hidden className="absolute top-5 left-[7px] h-full w-px bg-border" />
      ) : null}
      <span
        aria-hidden
        className="mt-1 size-3.5 shrink-0 rounded-full ring-4 ring-background"
        style={{ backgroundColor: color }}
      />
      <div className="flex flex-1 flex-col gap-1">
        <div className="flex items-center justify-between gap-3">
          <span className="font-medium text-foreground">{stageLabel(step.stage)}</span>
          <time className="text-xs text-muted-foreground tabular-nums">
            {formatDateTime(step.occurredAt)}
          </time>
        </div>
        <div className="flex items-center gap-2 text-xs">
          <span style={{ color }}>{CHAIN_TEXT[step.chainStatus] ?? step.chainStatus}</span>
          {step.txHash ? (
            <button
              type="button"
              onClick={() => onSelectHash(step.txHash as string)}
              className="font-mono text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
            >
              {truncateHash(step.txHash, 8, 6)}
            </button>
          ) : (
            <span className="text-muted-foreground">— ожидает записи в сеть</span>
          )}
        </div>
        {step.errorMessage ? (
          <p className="text-xs text-status-failed">{step.errorMessage}</p>
        ) : null}
      </div>
    </li>
  )
}
