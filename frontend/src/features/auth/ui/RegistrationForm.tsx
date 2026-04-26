import {
  Button,
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  Input,
  PasswordInput,
} from '@/shared/ui'

import { useRegistrationForm } from '../model/useRegistrationForm'
import { AuthFormShell } from './AuthFormShell'

type RegistrationFormProps = {
  onBack: () => void
  onSuccess: () => void
}

export function RegistrationForm({
  onBack,
  onSuccess,
}: RegistrationFormProps) {
  const {
    form: {
      formState: { errors },
      register,
    },
    isPending,
    onSubmit,
  } = useRegistrationForm({
    onSuccess,
  })

  return (
    <AuthFormShell title="Регистрация">
      <form onSubmit={onSubmit}>
        <FieldGroup className="gap-5">
          <Field>
            <FieldLabel htmlFor="registration-username">Почта</FieldLabel>
            <Input
              id="registration-username"
              autoComplete="username"
              aria-invalid={Boolean(errors.username)}
              placeholder="Введите вашу почту"
              {...register('username')}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="registration-password">Пароль</FieldLabel>
            <FieldDescription className="min-h-5">
              Пароль должен содержать не менее 8 символов
            </FieldDescription>
            <PasswordInput
              id="registration-password"
              autoComplete="new-password"
              aria-invalid={Boolean(errors.password)}
              placeholder="Придумайте пароль"
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
