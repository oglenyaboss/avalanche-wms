export interface AreaSeries {
  key: string
  color: string
  counts: number[]
}

interface AreaChartProps {
  series: AreaSeries[]
  height?: number
}

// viewBox units. preserveAspectRatio="none" stretches fills to the container
// width; strokes use vector-effect="non-scaling-stroke" so they stay crisp.
const VB_W = 100
const VB_H = 100
const HEADROOM = 1.15

// A stacked-area chart over a fixed day axis, drawn as pure SVG. Each band sits
// on the cumulative total of the bands below it; a crisp top line caps each.
export function AreaChart({ series, height = 200 }: AreaChartProps) {
  const n = series[0]?.counts.length ?? 0
  if (n === 0) {
    return <div style={{ height }} aria-hidden="true" />
  }

  const totals = new Array<number>(n).fill(0)
  for (const s of series) {
    for (let i = 0; i < n; i++) totals[i] += s.counts[i] ?? 0
  }
  const maxY = Math.max(1, ...totals) * HEADROOM

  const x = (i: number) => (n === 1 ? VB_W / 2 : (i / (n - 1)) * VB_W)
  const y = (v: number) => VB_H - (v / maxY) * VB_H

  // Build stacked bands bottom-up.
  const baseline = new Array<number>(n).fill(0)
  const bands = series.map((s) => {
    const top = baseline.map((b, i) => b + (s.counts[i] ?? 0))
    const topPts = top.map((v, i) => `${x(i)},${y(v)}`)
    const basePts = baseline.map((v, i) => `${x(i)},${y(v)}`).reverse()
    const area = `M ${topPts.join(' L ')} L ${basePts.join(' L ')} Z`
    const line = `M ${topPts.join(' L ')}`
    for (let i = 0; i < n; i++) baseline[i] = top[i]
    return { key: s.key, color: s.color, area, line }
  })

  const gridlines = [0.25, 0.5, 0.75, 1]

  return (
    <svg
      viewBox={`0 0 ${VB_W} ${VB_H}`}
      preserveAspectRatio="none"
      className="w-full"
      style={{ height }}
      role="presentation"
    >
      {gridlines.map((g) => (
        <line
          key={g}
          x1={0}
          x2={VB_W}
          y1={VB_H - g * VB_H}
          y2={VB_H - g * VB_H}
          stroke="var(--border)"
          strokeWidth={1}
          strokeDasharray="2 3"
          vectorEffect="non-scaling-stroke"
        />
      ))}
      {/* Light, translucent washes keep the stacked bands from collapsing into
          one dark mass; the crisp top stroke is what differentiates each stage. */}
      {bands.map((b) => (
        <path key={b.key} d={b.area} fill={b.color} fillOpacity={0.16} />
      ))}
      {bands.map((b) => (
        <path
          key={`${b.key}-line`}
          d={b.line}
          fill="none"
          stroke={b.color}
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
          vectorEffect="non-scaling-stroke"
        />
      ))}
    </svg>
  )
}
