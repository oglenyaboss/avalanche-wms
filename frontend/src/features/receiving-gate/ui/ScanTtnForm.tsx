import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect } from 'react'
import { useForm, useWatch } from 'react-hook-form'

import {
  Alert,
  AlertDescription,
  Button,
  Field,
  FieldLabel,
  Input,
} from '@/shared/ui'

import { scanFormSchema, type ScanFormValues } from '../model/schema'

interface ScanTtnFormProps {
  onScan: (code: string) => Promise<void> | void
  isScanning: boolean
  successMessage: string | null
  /** True while the error dialog is open and traps focus. */
  isBlocked: boolean
}

export function ScanTtnForm({
  onScan,
  isScanning,
  successMessage,
  isBlocked,
}: ScanTtnFormProps) {
  const form = useForm<ScanFormValues>({
    resolver: zodResolver(scanFormSchema),
    defaultValues: { code: '' },
  })
  const code = useWatch({ control: form.control, name: 'code' })
  const { setFocus } = form
  const error = form.formState.errors.code

  // Restore focus to the scanner input on mount and after the error dialog
  // closes (e.g. when a terminal error or a closed shipment drops us back here).
  useEffect(() => {
    if (!isBlocked && !isScanning) {
      setFocus('code')
    }
  }, [isBlocked, isScanning, setFocus])

  const onSubmit = form.handleSubmit(async (values) => {
    await onScan(values.code.trim())
    form.reset({ code: '' })
  })

  return (
    <section className="flex flex-col gap-4">
      {successMessage ? (
        <Alert variant="success">
          <AlertDescription className="col-span-2">
            {successMessage}
          </AlertDescription>
        </Alert>
      ) : null}

      <h2 className="font-heading text-xl font-semibold">
        Отсканируйте штрих код ТТН
      </h2>

      <form onSubmit={onSubmit} className="flex flex-col gap-2">
        <Field
          orientation="horizontal"
          className="items-start [&>[data-slot=button]]:shrink-0"
        >
          <FieldLabel htmlFor="ttn-code" className="sr-only">
            Штрих-код ТТН
          </FieldLabel>
          <Input
            id="ttn-code"
            autoComplete="off"
            enterKeyHint="send"
            placeholder="Данные со штрих-кода"
            aria-invalid={Boolean(error)}
            aria-describedby={error ? 'ttn-code-error' : undefined}
            {...form.register('code')}
          />
          <Button type="submit" disabled={!code.trim() || isScanning}>
            {isScanning ? 'Подождите...' : 'Подтвердить'}
          </Button>
        </Field>
        {error ? (
          <p id="ttn-code-error" className="text-sm text-destructive">
            {error.message}
          </p>
        ) : null}
        <p className="text-sm text-muted-foreground">
          В случае неполадки сканирования, введите ШК вручную
        </p>
      </form>
    </section>
  )
}
