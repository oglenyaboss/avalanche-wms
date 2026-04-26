import {
  Button,
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  Input,
  PasswordInput,
} from '@/shared/ui'

import { useLoginForm } from '../model/useLoginForm'
import { AuthFormShell } from './AuthFormShell'
import { AuthStatusAlert } from './AuthStatusAlert'

type LoginFormProps = {
  onClearSuccessAlert: () => void
  onPasswordResetClick: () => void
  onRegistrationClick: () => void
  successAlert: {
    description: string
    title: string
  } | null
}

export function LoginForm({
  onClearSuccessAlert,
  onPasswordResetClick,
  onRegistrationClick,
  successAlert,
}: LoginFormProps) {
  const {
    form: {
      formState: { errors },
      register,
    },
    isPending,
    loginError,
    onSubmit,
  } = useLoginForm({
    onClearSuccessMessage: onClearSuccessAlert,
  })

  const status = loginError ? (
    <AuthStatusAlert
      variant="destructive"
      title="Неправильные данные"
      description={
        <>
          Пожалуйста, проверьте корректность вводимых почты и пароля.
          <br />
          Убедитесь, что аккаунт, зарегистрированный под данной почтой,
          существует.
        </>
      }
    />
  ) : successAlert ? (
    <AuthStatusAlert
      variant="success"
      title={successAlert.title}
      description={successAlert.description}
    />
  ) : null

  return (
    <AuthFormShell title="Вход в систему" status={status}>
      <form onSubmit={onSubmit}>
        <FieldGroup className="gap-5">
          <Field>
            <FieldLabel htmlFor="username">Почта</FieldLabel>
            <Input
              id="username"
              autoComplete="username"
              aria-invalid={Boolean(errors.username)}
              placeholder="Введите вашу почту"
              {...register('username')}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="password">Пароль</FieldLabel>
            <FieldDescription className="min-h-5">
              Пароль должен содержать не менее 8 символов
            </FieldDescription>
            <PasswordInput
              id="password"
              autoComplete="current-password"
              aria-invalid={Boolean(errors.password)}
              placeholder="Введите ваш пароль"
              {...register('password')}
            />
          </Field>
          <Field
            orientation="horizontal"
            className="[&>[data-slot=button]]:min-w-0 [&>[data-slot=button]]:flex-1"
          >
            <Button variant="outline" type="button" onClick={onRegistrationClick}>
              Регистрация
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending ? 'Подождите...' : 'Войти'}
            </Button>
          </Field>
          <Button variant="link" type="button" onClick={onPasswordResetClick}>
            Забыли пароль?
          </Button>
        </FieldGroup>
      </form>
    </AuthFormShell>
  )
}
