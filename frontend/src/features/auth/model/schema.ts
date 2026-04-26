import { z } from 'zod'

export const usernameSchema = z
  .string()
  .trim()
  .min(1, 'Введите почту')
  .email('Введите корректную почту')

export const passwordSchema = z
  .string()
  .min(8, 'Пароль должен содержать не менее 8 символов')
  .max(72, 'Пароль должен быть не длиннее 72 символов')

export const authCredentialsSchema = z.object({
  username: usernameSchema,
  password: passwordSchema,
})

export const loginSchema = authCredentialsSchema
export const registrationSchema = authCredentialsSchema
export const passwordResetSchema = authCredentialsSchema

export type LoginFormValues = z.infer<typeof loginSchema>
export type RegistrationFormValues = z.infer<typeof registrationSchema>
export type PasswordResetFormValues = z.infer<typeof passwordResetSchema>
