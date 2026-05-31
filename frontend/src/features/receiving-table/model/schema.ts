import { z } from 'zod'

// A canonical UUID v1-v5. The backend's scan-cargoplace and scan-buffer accept
// ONLY a UUID (it calls uuid.Parse and rejects anything else with 400). We
// validate client-side so the operator gets immediate feedback instead of a
// round-trip; a hardware scanner reading the UUID label produces this shape.
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

// Free-text scan codes (box barcode, product barcode, product QR). The API
// caps these string fields at 256 characters.
export const scanCodeSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите код')
    .max(256, 'Код не должен превышать 256 символов'),
})

export const cargoplaceUuidSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите код')
    .regex(UUID_PATTERN, 'Ожидается идентификатор грузоместа (UUID)'),
})

export const bufferUuidSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите код')
    .regex(UUID_PATTERN, 'Ожидается идентификатор ячейки буфера (UUID)'),
})

export type ScanFormValues = z.infer<typeof scanCodeSchema>

// All three schemas are structurally identical Zod objects ({ code: string }),
// so the scan form can accept any of them under one type.
export type ScanFieldSchema = typeof scanCodeSchema
