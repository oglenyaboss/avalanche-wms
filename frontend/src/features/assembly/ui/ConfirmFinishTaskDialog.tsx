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

interface ConfirmFinishTaskDialogProps {
  open: boolean
  onConfirm: () => void
  onCancel: () => void
}

// Confirms the operator wants to interrupt (abandon) the current pick batch
// without placing it in a shipping buffer. No request fires here — it only
// resets the flow to the destination picker — so the auto-closing
// AlertDialogAction is fine.
export function ConfirmFinishTaskDialog({
  open,
  onConfirm,
  onCancel,
}: ConfirmFinishTaskDialogProps) {
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
            Прервать сборку? Уже собранные товары останутся за вами —
            отсканируйте буфер отгрузки позже, чтобы завершить.
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
