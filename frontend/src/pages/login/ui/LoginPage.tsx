import { LoginForm, PasswordResetForm, RegistrationForm } from '@/features/auth'

import { useLoginPageState } from '../model/useLoginPageState'

export function LoginPage() {
  const {
    clearSuccessAlert,
    handlePasswordResetSuccess,
    handleRegistrationSuccess,
    mode,
    openLogin,
    openPasswordReset,
    openRegistration,
    successAlert,
  } = useLoginPageState()

  return (
    <main className="flex min-h-screen flex-col items-center px-6 py-12">
      {mode === 'login' ? (
        <LoginForm
          onClearSuccessAlert={clearSuccessAlert}
          onPasswordResetClick={openPasswordReset}
          onRegistrationClick={openRegistration}
          successAlert={successAlert}
        />
      ) : null}
      {mode === 'registration' ? (
        <RegistrationForm
          onBack={openLogin}
          onSuccess={handleRegistrationSuccess}
        />
      ) : null}
      {mode === 'password-reset' ? (
        <PasswordResetForm
          onBack={openLogin}
          onSuccess={handlePasswordResetSuccess}
        />
      ) : null}
    </main>
  )
}
