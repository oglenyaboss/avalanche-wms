import { describe, expect, it } from 'vitest'

import {
  chainStatusLabel,
  formatDayShort,
  formatNumber,
  formatPercent,
  lifecycleLabel,
  sharePercent,
  stageLabel,
  truncateHash,
} from './format'

describe('formatNumber', () => {
  it('leaves sub-thousand values ungrouped', () => {
    expect(formatNumber(42)).toBe('42')
  })

  it('groups thousands (separator-agnostic)', () => {
    // ru-RU uses a no-break/narrow space; assert digits regardless of which.
    expect(formatNumber(1234).replace(/\s/gu, '')).toBe('1234')
    expect(formatNumber(1234).length).toBeGreaterThan(4)
  })
})

describe('formatPercent', () => {
  it('renders one decimal with a comma separator', () => {
    expect(formatPercent(0.8372)).toBe('83,7%')
  })

  it('renders zero as 0,0%', () => {
    expect(formatPercent(0)).toBe('0,0%')
  })

  it('clamps values above 1 to 100%', () => {
    expect(formatPercent(1.5)).toBe('100,0%')
  })

  it('clamps negative values to 0%', () => {
    expect(formatPercent(-0.3)).toBe('0,0%')
  })
})

describe('sharePercent', () => {
  it('returns 0 when the total is 0 (no divide-by-zero)', () => {
    expect(sharePercent(5, 0)).toBe(0)
  })

  it('computes a percentage share', () => {
    expect(sharePercent(1, 4)).toBe(25)
  })
})

describe('truncateHash', () => {
  it('renders a dash for null', () => {
    expect(truncateHash(null)).toBe('—')
  })

  it('middle-truncates a long hash', () => {
    const hash = '0x' + 'a'.repeat(64)
    const out = truncateHash(hash)
    expect(out.startsWith('0x')).toBe(true)
    expect(out).toContain('…')
    expect(out.length).toBeLessThan(hash.length)
  })

  it('leaves short values intact', () => {
    expect(truncateHash('0xabcd')).toBe('0xabcd')
  })
})

describe('formatDayShort', () => {
  it('renders DD.MM from an ISO date', () => {
    expect(formatDayShort('2026-05-31')).toBe('31.05')
  })

  it('passes through an unexpected shape', () => {
    expect(formatDayShort('whenever')).toBe('whenever')
  })
})

describe('labels', () => {
  it('translates chain statuses', () => {
    expect(chainStatusLabel('committed')).toBe('Подтверждено')
    expect(chainStatusLabel('failed')).toBe('Ошибка')
  })

  it('translates known stages and passes through unknown ones', () => {
    expect(stageLabel('picking')).toBe('Сборка')
    expect(stageLabel('mystery')).toBe('mystery')
  })

  it('translates lifecycle statuses and passes through unknown ones', () => {
    expect(lifecycleLabel('READY_TO_SHIP')).toBe('К отгрузке')
    expect(lifecycleLabel('WHATEVER')).toBe('WHATEVER')
  })
})
