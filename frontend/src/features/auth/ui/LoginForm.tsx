import {
  Button,
  Field,
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
      title="Не удалось войти"
      description={loginError}
    />
  ) : successAlert ? (
    <AuthStatusAlert
      variant="success"
      title={successAlert.title}
      description={successAlert.description}
    />
  ) : null

  return (
    <AuthFormShell
      title="Вход в систему"
      description="Войдите, чтобы продолжить работу со складом"
      status={status}
    >
      <form onSubmit={onSubmit}>
        <FieldGroup className="gap-5">
          <Field>
            <FieldLabel htmlFor="username">Логин</FieldLabel>
            <Input
              id="username"
              autoComplete="username"
              aria-invalid={Boolean(errors.username)}
              placeholder="Введите ваш логин"
              {...register('username')}
            />
          </Field>
          <Field>
            <div className="flex items-baseline justify-between gap-2">
              <FieldLabel htmlFor="password">Пароль</FieldLabel>
              <Button
                variant="link"
                type="button"
                onClick={onPasswordResetClick}
                className="text-xs"
              >
                Забыли пароль?
              </Button>
            </div>
            <PasswordInput
              id="password"
              autoComplete="current-password"
              aria-invalid={Boolean(errors.password)}
              placeholder="Введите ваш пароль"
              {...register('password')}
            />
          </Field>
          <Button type="submit" disabled={isPending} className="w-full">
            {isPending ? 'Подождите...' : 'Войти'}
          </Button>
        </FieldGroup>
      </form>

      <p className="mt-6 text-center text-sm text-muted-foreground">
        Нет аккаунта?{' '}
        <button
          type="button"
          onClick={onRegistrationClick}
          className="font-medium text-foreground underline-offset-4 hover:underline"
        >
          Создать аккаунт
        </button>
      </p>
    </AuthFormShell>
  )
}
