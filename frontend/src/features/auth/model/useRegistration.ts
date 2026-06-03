import { useMutation } from '@tanstack/react-query'

import { submitTemporaryRegistration } from '../api/authStubApi'
import type { RegistrationFormValues } from './schema'

export function useRegistration() {
  const mutation = useMutation({
    mutationFn: (values: RegistrationFormValues) =>
      submitTemporaryRegistration(values),
    mutationKey: ['auth', 'registration'],
    retry: false,
  })

  return {
    isPending: mutation.isPending,
    registerUser: mutation.mutateAsync,
  }
}
