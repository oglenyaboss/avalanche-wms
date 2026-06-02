import { z } from 'zod'

// A canonical UUID v1–v5. /shipping/scan-buffer and /shipping/ship accept the
// buffer bin ONLY as a UUID (uuid.Parse, 400 otherwise). Validating client-side
// gives the operator immediate feedback; a hardware scanner reading the bin's
// UUID label produces this shape.
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

export const bufferBinUuidSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите код')
    .max(36, 'Ожидается UUID')
    .regex(UUID_PATTERN, 'Ожидается идентификатор буфера отгрузки (UUID)'),
})

// The driver's QR carries a dispatch code (e.g. "DSP-2026-0421-001") — free text,
// not a UUID. The API caps string fields at 256 characters.
export const dispatchCodeSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите код рейса')
    .max(256, 'Код не должен превышать 256 символов'),
})

// Product QR codes are free text (e.g. "QR-2026-0009"). In spot mode the QR is
// resolved to a product_id client-side from the loaded buffer products.
export const productQrSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите QR-код')
    .max(256, 'Код не должен превышать 256 символов'),
})

export type ScanFormValues = z.infer<typeof productQrSchema>

// Every shipping scan form is a structurally identical Zod object ({ code:
// string }), so the shared ScanForm can accept any of them under one type.
export type ScanFieldSchema = typeof productQrSchema
