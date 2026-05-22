import {
  Button,
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  Input,
  PasswordInput,
} from '@/shared/ui'

import { usePasswordResetForm } from '../model/usePasswordResetForm'
import { AuthFormShell } from './AuthFormShell'

type PasswordResetFormProps = {
  onBack: () => void
  onSuccess: () => void
}

export function PasswordResetForm({
  onBack,
  onSuccess,
}: PasswordResetFormProps) {
  const {
    form: {
      formState: { errors },
      register,
    },
    isPending,
    onSubmit,
  } = usePasswordResetForm({
    onSuccess,
  })

  return (
    <AuthFormShell title="Сброс пароля">
      <form onSubmit={onSubmit}>
        <FieldGroup className="gap-5">
          <Field>
            <FieldLabel htmlFor="password-reset-username">Логин</FieldLabel>
            <Input
              id="password-reset-username"
              autoComplete="username"
              aria-invalid={Boolean(errors.username)}
              placeholder="Введите ваш логин"
              {...register('username')}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="password-reset-password">Пароль</FieldLabel>
            <FieldDescription className="min-h-5">
              Пароль должен содержать не менее 6 символов
            </FieldDescription>
            <PasswordInput
              id="password-reset-password"
              autoComplete="new-password"
              aria-invalid={Boolean(errors.password)}
              placeholder="Придумайте новый пароль"
              {...register('password')}
            />
          </Field>
          <Field
            orientation="horizontal"
            className="[&>[data-slot=button]]:min-w-0 [&>[data-slot=button]]:flex-1"
          >
            <Button variant="outline" type="button" onClick={onBack}>
              Назад
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending ? 'Подождите...' : 'Подтвердить'}
            </Button>
          </Field>
          <div className="min-h-6" />
        </FieldGroup>
      </form>
    </AuthFormShell>
  )
}
