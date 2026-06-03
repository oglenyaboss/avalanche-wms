// Formatting + labeling helpers now live in shared/lib (used by both analytics
// and traceability). Re-exported here so the analytics dashboard keeps importing
// them from its own model.
export {
  formatNumber,
  formatPercent,
  sharePercent,
  formatDateTime,
  formatDayShort,
  truncateHash,
  lifecycleLabel,
  chainStatusLabel,
  stageLabel,
} from '@/shared/lib'
