import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
  Button,
} from '@/shared/ui'

import { WarningIcon } from './icons'

interface ConfirmCloseDialogProps {
  open: boolean
  isPending: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmCloseDialog({
  open,
  isPending,
  onConfirm,
  onCancel,
}: ConfirmCloseDialogProps) {
  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          onCancel()
        }
      }}
    >
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogMedia className="bg-amber-100 text-amber-500">
            <WarningIcon />
          </AlertDialogMedia>
          <AlertDialogTitle>Предупреждение</AlertDialogTitle>
          <AlertDialogDescription>
            Вы подтверждаете, что больше нечего принимать по ТТН?
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={isPending}>Нет</AlertDialogCancel>
          {/* Plain button (not AlertDialogAction) so the dialog stays open and
              shows the pending state until the request resolves and the
              orchestrator closes it. */}
          <Button type="button" onClick={onConfirm} disabled={isPending}>
            {isPending ? 'Закрытие...' : 'Да'}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
