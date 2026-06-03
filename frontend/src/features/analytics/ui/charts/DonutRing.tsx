import type { ReactNode } from 'react'

export interface RingSegment {
  key: string
  value: number
  color: string
}

interface DonutRingProps {
  segments: RingSegment[]
  size?: number
  thickness?: number
  children?: ReactNode
}

// A stacked donut drawn with pathLength=100 so each arc's dash length is its
// percentage directly. The SVG is rotated -90° so segments start at 12 o'clock.
// When there is no data, only the track renders — an honest empty ring.
export function DonutRing({
  segments,
  size = 184,
  thickness = 16,
  children,
}: DonutRingProps) {
  const radius = (size - thickness) / 2
  const center = size / 2
  const total = segments.reduce((sum, s) => sum + s.value, 0)

  let offset = 0
  return (
    <div
      className="relative shrink-0"
      style={{ width: size, height: size }}
    >
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        className="-rotate-90"
        role="presentation"
      >
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke="var(--muted)"
          strokeWidth={thickness}
          pathLength={100}
        />
        {total > 0 &&
          segments.map((seg) => {
            const pct = (seg.value / total) * 100
            if (pct <= 0) return null
            const dash = `${pct} ${100 - pct}`
            const node = (
              <circle
                key={seg.key}
                cx={center}
                cy={center}
                r={radius}
                fill="none"
                stroke={seg.color}
                strokeWidth={thickness}
                strokeDasharray={dash}
                strokeDashoffset={-offset}
                pathLength={100}
                strokeLinecap="butt"
              />
            )
            offset += pct
            return node
          })}
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center text-center">
        {children}
      </div>
    </div>
  )
}
