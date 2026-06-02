import { formatNumber, formatPercent } from '../model/format'
import type { OnchainReport, SummaryReport } from '../model/types'
import { STATUS_COLOR } from './colors'
import { CubeIcon } from './icons'
import { KpiStat } from './KpiStat'

interface KpiStripProps {
  onchain: OnchainReport
  summary: SummaryReport
}

// Four headline metrics, onchain-led because the chain audit is the page's
// reason for being.
export function KpiStrip({ onchain, summary }: KpiStripProps) {
  return (
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <KpiStat
        label="Всего событий"
        value={formatNumber(onchain.totalEvents)}
        hint={`сегодня: ${formatNumber(summary.eventsToday)}`}
        icon={<CubeIcon className="size-4" />}
      />
      <KpiStat
        label="Подтверждено"
        value={formatNumber(onchain.committed)}
        hint={`${formatPercent(onchain.confirmationRate)} аудита в сети`}
        accent={STATUS_COLOR.committed}
      />
      <KpiStat
        label="В ожидании"
        value={formatNumber(onchain.pending)}
        hint="ожидают записи в блокчейн"
        accent={STATUS_COLOR.pending}
      />
      <KpiStat
        label="Ошибки"
        value={formatNumber(onchain.failed)}
        hint={onchain.failed > 0 ? 'требуют внимания' : 'ошибок нет'}
        accent={STATUS_COLOR.failed}
      />
    </div>
  )
}
