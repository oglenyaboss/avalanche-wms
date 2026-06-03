import { z } from 'zod'

// Both the TTN and cargoplace inputs are a single scanned code. The API caps
// ttn_code / cargoplace_code at 256 characters.
export const scanFormSchema = z.object({
  code: z
    .string()
    .trim()
    .min(1, 'Введите код')
    .max(256, 'Код не должен превышать 256 символов'),
})

export type ScanFormValues = z.infer<typeof scanFormSchema>
