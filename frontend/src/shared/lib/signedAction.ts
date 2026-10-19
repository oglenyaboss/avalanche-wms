// Signed-action reject reasons as reported by the chain (reject_reason codes),
// shared by the passkey confirmation flow and the receiving close flow.

export const REJECT_REASON_LABELS: Record<number, string> = {
  1: 'Ключ подписанта неизвестен реестру',
  2: 'Ключ подписанта отозван',
  3: 'Подпись не прошла проверку в цепи',
  4: 'Неверный тип действия',
  5: 'Подписант не админ организации',
  6: 'Ключ уже зарегистрирован в реестре',
  7: 'Некорректный публичный ключ',
  8: 'Ключ из другой организации',
  9: 'Ключ не принадлежит оператору',
  10: 'Действие без товаров',
  11: 'Товары не по порядку',
  12: 'Товар не в нужном статусе',
}

export function rejectReasonText(code: number | null): string {
  if (code === null) return 'Причина неизвестна'
  return REJECT_REASON_LABELS[code] ?? `Код ${code}`
}
