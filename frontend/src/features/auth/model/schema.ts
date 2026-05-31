import { z } from 'zod'

export const usernameSchema = z
  .string()
  .trim()
  .min(1, 'Введите логин')

export const loginPasswordSchema = z
  .string()
  .min(1, 'Введите пароль')
  .max(72, 'Пароль должен быть не длиннее 72 символов')

export const managedPasswordSchema = z
  .string()
  .min(6, 'Пароль должен содержать не менее 6 символов')
  .max(72, 'Пароль должен быть не длиннее 72 символов')

// Login only checks the field is present; password strength is the backend's
// concern here. Strength rules live on registration (managedPasswordSchema).
export const loginSchema = z.object({
  username: usernameSchema,
  password: loginPasswordSchema,
})

export const registrationSchema = z.object({
  username: usernameSchema,
  password: managedPasswordSchema,
})

// TODO: Replace with the real password reset contract once the backend flow exists.
export const passwordResetSchema = z.object({
  username: usernameSchema,
  password: managedPasswordSchema,
})

export type LoginFormValues = z.infer<typeof loginSchema>
export type RegistrationFormValues = z.infer<typeof registrationSchema>
export type PasswordResetFormValues = z.infer<typeof passwordResetSchema>
