import { useState } from 'react'

type LoginPageMode = 'login' | 'password-reset' | 'registration'
type LoginPageSuccessAlert = {
  description: string
  title: string
}

export function useLoginPageState() {
  const [mode, setMode] = useState<LoginPageMode>('login')
  const [successAlert, setSuccessAlert] = useState<LoginPageSuccessAlert | null>(
    null,
  )

  function clearSuccessAlert() {
    setSuccessAlert(null)
  }

  function openLogin() {
    setMode('login')
    clearSuccessAlert()
  }

  function openRegistration() {
    setMode('registration')
    clearSuccessAlert()
  }

  function openPasswordReset() {
    setMode('password-reset')
    clearSuccessAlert()
  }

  function handleRegistrationSuccess() {
    setMode('login')
    setSuccessAlert({
      description:
        'Ваш аккаунт был успешно зарегистрирован. Теперь вы можете вернуться к процессу входа в систему.',
      title: 'Успех!',
    })
  }

  function handlePasswordResetSuccess() {
    setMode('login')
    setSuccessAlert({
      description:
        'Ваш пароль был успешно изменен. Теперь вы можете вернуться к процессу входа в систему.',
      title: 'Успех!',
    })
  }

  return {
    clearSuccessAlert,
    handlePasswordResetSuccess,
    handleRegistrationSuccess,
    mode,
    openLogin,
    openPasswordReset,
    openRegistration,
    successAlert,
  }
}
