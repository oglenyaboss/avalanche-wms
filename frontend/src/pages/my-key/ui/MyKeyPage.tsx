import { MyPasskeyCard } from '@/features/passkey'

export function MyKeyPage() {
  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-6 px-6 py-10">
      <header className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold tracking-tight">Мой ключ</h1>
        <p className="text-muted-foreground">
          Ключ хранится на этом устройстве, на сервер передаётся только публичная часть.
        </p>
      </header>
      <MyPasskeyCard />
    </div>
  )
}
