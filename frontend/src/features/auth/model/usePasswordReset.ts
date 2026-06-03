import { useMutation } from '@tanstack/react-query'

import { submitTemporaryPasswordReset } from '../api/authStubApi'
import type { PasswordResetFormValues } from './schema'

export function usePasswordReset() {
  const mutation = useMutation({
    mutationFn: (values: PasswordResetFormValues) =>
      submitTemporaryPasswordReset(values),
    mutationKey: ['auth', 'password-reset'],
    retry: false,
  })

  return {
    isPending: mutation.isPending,
    resetPassword: mutation.mutateAsync,
  }
}
