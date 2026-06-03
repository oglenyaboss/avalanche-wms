import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'

import { registrationSchema, type RegistrationFormValues } from './schema'
import { useRegistration } from './useRegistration'

type UseRegistrationFormParams = {
  onSuccess: () => void
}

export function useRegistrationForm({
  onSuccess,
}: UseRegistrationFormParams) {
  const { isPending, registerUser } = useRegistration()
  const form = useForm<RegistrationFormValues>({
    resolver: zodResolver(registrationSchema),
    defaultValues: {
      username: '',
      password: '',
    },
  })

  const onSubmit = form.handleSubmit(async (values) => {
    await registerUser(values)
    onSuccess()
  })

  return {
    form,
    isPending,
    onSubmit,
  }
}
