import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect, type ReactNode } from 'react'
import { useForm, useWatch } from 'react-hook-form'

import { Button, Field, FieldLabel, Input } from '@/shared/ui'

import type { ScanFieldSchema, ScanFormValues } from '../model/schema'

interface ScanFormProps {
  title: string
  inputId: string
  srLabel: string
  placeholder: string
  submitLabel: string
  pendingLabel?: string
  manualHint?: string
  /** Optional content shown between the title and the input (e.g. a prompt). */
  helper?: ReactNode
  onScan: (value: string) => Promise<void> | void
  isScanning: boolean
  /** True while the error dialog is open and traps focus. */
  isBlocked: boolean
  schema: ScanFieldSchema
}

// A single scanned-code input shared by every shipping step. The scanner is a
// keyboard wedge: it types the code and presses Enter, so the input must stay
// focused and clear itself after each scan.
export function ScanForm({
  title,
  inputId,
  srLabel,
  placeholder,
  submitLabel,
  pendingLabel = 'Подождите...',
  manualHint = 'В случае неполадки сканирования, введите код вручную',
  helper,
  onScan,
  isScanning,
  isBlocked,
  schema,
}: ScanFormProps) {
  const form = useForm<ScanFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { code: '' },
  })
  const code = useWatch({ control: form.control, name: 'code' })
  const { setFocus } = form
  const error = form.formState.errors.code

  // Restore focus to the scanner input on mount and once the error dialog
  // closes, so the next code can be scanned hands-free.
  useEffect(() => {
    if (!isBlocked && !isScanning) {
      setFocus('code')
    }
  }, [isBlocked, isScanning, setFocus])

  const onSubmit = form.handleSubmit(async (values) => {
    await onScan(values.code.trim())
    // Clear for the next scan. If a terminal transition unmounted this form, the
    // reset is a harmless no-op.
    form.reset({ code: '' })
  })

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-2">
      <h3 className="font-medium">{title}</h3>
      {helper}
      <Field
        orientation="horizontal"
        className="items-start [&>[data-slot=button]]:shrink-0"
      >
        <FieldLabel htmlFor={inputId} className="sr-only">
          {srLabel}
        </FieldLabel>
        <Input
          id={inputId}
          autoComplete="off"
          enterKeyHint="send"
          placeholder={placeholder}
          aria-invalid={Boolean(error)}
          aria-describedby={error ? `${inputId}-error` : undefined}
          {...form.register('code')}
        />
        <Button type="submit" disabled={!code.trim() || isScanning}>
          {isScanning ? pendingLabel : submitLabel}
        </Button>
      </Field>
      {error ? (
        <p id={`${inputId}-error`} className="text-sm text-destructive">
          {error.message}
        </p>
      ) : null}
      <p className="text-sm text-muted-foreground">{manualHint}</p>
    </form>
  )
}
