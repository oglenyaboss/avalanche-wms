import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'

import { loginSchema, type LoginFormValues } from './schema'
import { useLogin } from './useLogin'

type UseLoginFormParams = {
  onClearSuccessMessage: () => void
}

export function useLoginForm({
  onClearSuccessMessage,
}: UseLoginFormParams) {
  const [loginError, setLoginError] = useState('')
  const { getErrorMessage, isPending, login, reset } = useLogin()
  const form = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: {
      username: '',
      password: '',
    },
  })

  const onSubmit = form.handleSubmit(async (values) => {
    try {
      setLoginError('')
      onClearSuccessMessage()
      reset()

      await login(values)
    } catch (error) {
      setLoginError(getErrorMessage(error))
    }
  })

  return {
    form,
    isPending,
    loginError,
    onSubmit,
  }
}
