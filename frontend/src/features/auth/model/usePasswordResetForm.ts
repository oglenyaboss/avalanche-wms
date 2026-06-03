import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'

import { passwordResetSchema, type PasswordResetFormValues } from './schema'
import { usePasswordReset } from './usePasswordReset'

type UsePasswordResetFormParams = {
  onSuccess: () => void
}

export function usePasswordResetForm({
  onSuccess,
}: UsePasswordResetFormParams) {
  const { isPending, resetPassword } = usePasswordReset()
  const form = useForm<PasswordResetFormValues>({
    resolver: zodResolver(passwordResetSchema),
    defaultValues: {
      username: '',
      password: '',
    },
  })

  const onSubmit = form.handleSubmit(async (values) => {
    await resetPassword(values)
    onSuccess()
  })

  return {
    form,
    isPending,
    onSubmit,
  }
}
