import { useMutation } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { useNavigate } from 'react-router'

import { useSessionStore } from '@/entities/session'
import { routes } from '@/shared/config'

import { loginRequest } from '../api/authApi'
import type { LoginFormValues } from './schema'

function getLoginErrorMessage(error: unknown) {
  if (isAxiosError(error) && error.response?.status === 401) {
    return 'Неверный логин или пароль'
  }

  return 'Не удалось выполнить вход'
}

export function useLogin() {
  const navigate = useNavigate()
  const startSession = useSessionStore((state) => state.startSession)
  const mutation = useMutation({
    mutationFn: (values: LoginFormValues) => loginRequest(values),
    mutationKey: ['auth', 'login'],
    retry: false,
    onSuccess: (tokens) => {
      startSession(tokens)
      navigate(routes.home, { replace: true })
    },
  })

  return {
    getErrorMessage: getLoginErrorMessage,
    isPending: mutation.isPending,
    login: mutation.mutateAsync,
    reset: mutation.reset,
  }
}
