import { LoginForm, PasswordResetForm, RegistrationForm } from '@/features/auth'
import { Logo } from '@/shared/ui'

import { useLoginPageState } from '../model/useLoginPageState'

const brandPoints = [
  'Каждая складская операция — событие в неизменяемом реестре',
  'Аудит подтверждается в сети Avalanche',
  'Сквозная прослеживаемость от приёмки до отгрузки',
]

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
    <main className="grid min-h-screen lg:grid-cols-[1.05fr_1fr]">
      {/* Brand panel — sells the on-chain story on the first screen the
          examiners see. Hidden on small screens. */}
      <aside className="relative hidden flex-col justify-between overflow-hidden bg-primary px-12 py-12 text-primary-foreground lg:flex">
        <div
          aria-hidden
          className="bg-dotgrid-invert pointer-events-none absolute inset-0"
        />
        <div
          aria-hidden
          className="pointer-events-none absolute -top-24 -right-24 size-80 rounded-full bg-status-committed/20 blur-3xl"
        />

        <div className="relative">
          <Logo size="lg" tone="invert" subtitle="Складской реестр" />
        </div>

        <div className="relative max-w-md">
          <h2 className="text-3xl leading-tight font-semibold tracking-tight text-balance">
            Складская система с неизменяемым реестром на блокчейне
          </h2>
          <ul className="mt-8 space-y-4">
            {brandPoints.map((point) => (
              <li
                key={point}
                className="flex items-start gap-3 text-sm text-primary-foreground/80"
              >
                <span className="mt-1.5 size-2 shrink-0 rounded-full bg-status-committed" />
                {point}
              </li>
            ))}
          </ul>
        </div>

        <div className="relative flex items-center gap-2 text-xs text-primary-foreground/55">
          <span className="size-1.5 animate-pulse rounded-full bg-status-committed" />
          Avalanche Subnet · аудит в реальном времени
        </div>
      </aside>

      {/* Form panel */}
      <div className="flex flex-col">
        <div className="px-6 pt-8 lg:hidden">
          <Logo />
        </div>
        <div className="flex flex-1 items-center justify-center px-6 py-12">
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
        </div>
      </div>
    </main>
  )
}
