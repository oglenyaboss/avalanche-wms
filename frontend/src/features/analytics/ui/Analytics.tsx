import type { ReactNode } from 'react'

import { getAnalyticsErrorMessage } from '../model/errors'
import { useAnalytics } from '../model/useAnalytics'
import { AnalyticsHeader } from './AnalyticsHeader'
import { FailedEventsCard } from './FailedEventsCard'
import { KpiStrip } from './KpiStrip'
import { OnchainHeroCard } from './OnchainHeroCard'
import { PipelineCard } from './PipelineCard'
import { RecentTxCard } from './RecentTxCard'
import { StageBreakdownCard } from './StageBreakdownCard'
import { ThroughputCard } from './ThroughputCard'
import { CardError, CardSkeleton, KpiStripSkeleton } from './states'

// Staged entrance: each block fades and rises with an increasing delay for one
// directed load sequence. Gated behind motion-safe so reduced-motion users get
// the content immediately, fully visible.
function Reveal({ children, delay = 0 }: { children: ReactNode; delay?: number }) {
  return (
    <div
      className="motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-3 motion-safe:duration-500"
      style={{ animationDelay: `${delay}ms`, animationFillMode: 'both' }}
    >
      {children}
    </div>
  )
}

export function Analytics() {
  const a = useAnalytics()
  const onRetry = a.refetchAll

  return (
    <div className="mx-auto w-full max-w-7xl space-y-7">
      <AnalyticsHeader
        lastUpdatedMs={a.lastUpdatedMs}
        isFetching={a.isFetching}
        onRefresh={a.refetchAll}
      />

      <Reveal>
        {a.onchain && a.summary ? (
          <KpiStrip onchain={a.onchain} summary={a.summary} />
        ) : (
          <KpiStripSkeleton />
        )}
      </Reveal>

      <Reveal delay={80}>
        <div className="grid gap-6 lg:grid-cols-3">
          <div className="lg:col-span-2">
            {a.onchain ? (
              <OnchainHeroCard onchain={a.onchain} />
            ) : a.onchainError ? (
              <CardError
                message={getAnalyticsErrorMessage(a.onchainError)}
                onRetry={onRetry}
              />
            ) : (
              <CardSkeleton lines={6} />
            )}
          </div>
          <div>
            {a.onchain ? (
              <StageBreakdownCard stages={a.onchain.byStage} />
            ) : a.onchainError ? (
              <CardError
                message={getAnalyticsErrorMessage(a.onchainError)}
                onRetry={onRetry}
              />
            ) : (
              <CardSkeleton lines={6} />
            )}
          </div>
        </div>
      </Reveal>

      <Reveal delay={160}>
        {a.throughput ? (
          <ThroughputCard
            throughput={a.throughput}
            days={a.throughputDays}
            onDaysChange={a.setThroughputDays}
          />
        ) : a.throughputError ? (
          <CardError
            message={getAnalyticsErrorMessage(a.throughputError)}
            onRetry={onRetry}
          />
        ) : (
          <CardSkeleton lines={6} />
        )}
      </Reveal>

      <Reveal delay={240}>
        <div className="grid gap-6 lg:grid-cols-3">
          {a.summary ? (
            <PipelineCard summary={a.summary} />
          ) : a.summaryError ? (
            <CardError
              message={getAnalyticsErrorMessage(a.summaryError)}
              onRetry={onRetry}
            />
          ) : (
            <CardSkeleton lines={6} />
          )}
          {a.onchain ? (
            <FailedEventsCard events={a.onchain.recentFailed} />
          ) : (
            <CardSkeleton lines={5} />
          )}
          {a.onchain ? (
            <RecentTxCard events={a.onchain.recentCommitted} />
          ) : (
            <CardSkeleton lines={5} />
          )}
        </div>
      </Reveal>
    </div>
  )
}
