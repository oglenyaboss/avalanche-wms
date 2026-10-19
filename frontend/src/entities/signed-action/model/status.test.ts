import { describe, expect, it } from 'vitest'

import { rejectReasonText } from '@/shared/lib'

import { operationStatusText } from './status'
import type { OperationPartState, OperationStatus } from './types'

function statusOf(
  states: Record<string, OperationPartState>,
  rejectReasons: Record<string, number | null> = {},
): OperationStatus {
  return {
    operationId: '01929c5e-op-1',
    parts: Object.entries(states).map(([actionId, state]) => ({
      actionId,
      state,
      rejectReason: rejectReasons[actionId] ?? null,
    })),
  }
}

describe('operationStatusText', () => {
  it('reports a fully applied operation as recorded into the chain', () => {
    expect(
      operationStatusText(statusOf({ 'a-1': 'APPLIED', 'a-2': 'APPLIED' }))
    ).toBe('Записано в цепь')
  })

  it('reports the chain reject reason for a rejected part', () => {
    expect(
      operationStatusText(statusOf({ 'a-1': 'APPLIED', 'a-2': 'REJECTED' }, { 'a-2': 12 }))
    ).toBe('Отклонено цепью: Товар не в нужном статусе')
  })

  it('reports a failed operation', () => {
    expect(operationStatusText(statusOf({ 'a-1': 'FAILED' }))).toBe(
      'Не удалось записать в цепь'
    )
  })

  it('reports in-flight parts as pending', () => {
    expect(
      operationStatusText(statusOf({ 'a-1': 'APPLIED', 'a-2': 'SENT' }))
    ).toBe('Отправлено в цепь, ждём подтверждения')
  })

  it('reports an operation without parts as pending, not applied', () => {
    expect(operationStatusText(statusOf({}))).toBe('Отправлено в цепь, ждём подтверждения')
  })
})

describe('rejectReasonText for receiving codes', () => {
  it('labels the receiving-specific reject reasons', () => {
    expect(rejectReasonText(10)).toBe('Действие без товаров')
    expect(rejectReasonText(11)).toBe('Товары не по порядку')
    expect(rejectReasonText(12)).toBe('Товар не в нужном статусе')
  })
})
