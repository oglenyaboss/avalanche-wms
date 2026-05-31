import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect } from 'react'
import { useForm, useWatch } from 'react-hook-form'

import { Button, Field, FieldLabel, Input } from '@/shared/ui'

import { scanFormSchema, type ScanFormValues } from '../model/schema'

interface ScanCargoplaceFormProps {
  onScan: (code: string) => Promise<void> | void
  isScanning: boolean
  /** True while the error dialog is open and traps focus. */
  isBlocked: boolean
}

export function ScanCargoplaceForm({
  onScan,
  isScanning,
  isBlocked,
}: ScanCargoplaceFormProps) {
  const form = useForm<ScanFormValues>({
    resolver: zodResolver(scanFormSchema),
    defaultValues: { code: '' },
  })
  const code = useWatch({ control: form.control, name: 'code' })
  const { setFocus } = form
  const error = form.formState.errors.code

  // Keep the scanner input focused so places can be scanned hands-free:
  // on mount, after each scan clears it, and once the error dialog closes.
  useEffect(() => {
    if (!isBlocked && !isScanning) {
      setFocus('code')
    }
  }, [isBlocked, isScanning, setFocus])

  const onSubmit = form.handleSubmit(async (values) => {
    await onScan(values.code.trim())
    // Clear for the next scan. If a terminal error unmounted this form, the
    // reset is a harmless no-op.
    form.reset({ code: '' })
  })

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-2">
      <Field
        orientation="horizontal"
        className="items-start [&>[data-slot=button]]:shrink-0"
      >
        <FieldLabel htmlFor="cargoplace-code" className="sr-only">
          Штрих-код грузоместа
        </FieldLabel>
        <Input
          id="cargoplace-code"
          autoComplete="off"
          enterKeyHint="send"
          placeholder="Данные со штрих-кода"
          aria-invalid={Boolean(error)}
          aria-describedby={error ? 'cargoplace-code-error' : undefined}
          {...form.register('code')}
        />
        <Button type="submit" disabled={!code.trim() || isScanning}>
          {isScanning ? 'Подождите...' : 'Подтвердить'}
        </Button>
      </Field>
      {error ? (
        <p id="cargoplace-code-error" className="text-sm text-destructive">
          {error.message}
        </p>
      ) : null}
      <p className="text-sm text-muted-foreground">
        В случае неполадки сканирования, введите ШК вручную
      </p>
    </form>
  )
}
