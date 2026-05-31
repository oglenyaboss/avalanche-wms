import { z } from 'zod'

// A canonical UUID v1-v5. The putaway scan-buffer and scan-storage-bin
// endpoints accept ONLY a UUID (uuid.Parse, 400 otherwise). We validate
// client-side so the operator gets immediate feedback instead of a round-trip;
// a hardware scanner reading the bin's UUID label produces this shape.
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

// Product QR codes are free text (e.g. "QR-2026-0001"). The QR is resolved to a
// product_id client-side from the buffer scan result, so it is not a UUID. The
// API caps string fields at 256 characters.
export const productQrSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите QR-код')
    .max(256, 'Код не должен превышать 256 символов'),
})

export const bufferUuidSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите код')
    .regex(UUID_PATTERN, 'Ожидается идентификатор ячейки буфера (UUID)'),
})

export const storageBinUuidSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите код')
    .regex(UUID_PATTERN, 'Ожидается идентификатор ячейки хранения (UUID)'),
})

export type ScanFormValues = z.infer<typeof productQrSchema>

// All three schemas are structurally identical Zod objects ({ code: string }),
// so the scan form can accept any of them under one type.
export type ScanFieldSchema = typeof productQrSchema
