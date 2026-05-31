import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from '@/shared/ui'

import { WarningIcon } from './icons'

interface ConfirmAcceptCargoplaceDialogProps {
  open: boolean
  onConfirm: () => void
  onCancel: () => void
}

// Confirms the operator is done adding boxes and wants to move the cargoplace's
// products to the buffer. No request fires here — it only switches the flow to
// the buffer phase — so AlertDialogAction (which auto-closes) is fine.
export function ConfirmAcceptCargoplaceDialog({
  open,
  onConfirm,
  onCancel,
}: ConfirmAcceptCargoplaceDialogProps) {
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
            Вы подтверждаете, что больше нечего принимать по грузоместу?
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Нет</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>Да</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
