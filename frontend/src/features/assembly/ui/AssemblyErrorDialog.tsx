import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from '@/shared/ui'

import { ErrorIcon } from './icons'

interface AssemblyErrorDialogProps {
  message: string | null
  onDismiss: () => void
}

export function AssemblyErrorDialog({
  message,
  onDismiss,
}: AssemblyErrorDialogProps) {
  return (
    <AlertDialog
      open={message !== null}
      onOpenChange={(open) => {
        if (!open) {
          onDismiss()
        }
      }}
    >
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogMedia className="bg-destructive/10 text-destructive">
            <ErrorIcon />
          </AlertDialogMedia>
          <AlertDialogTitle>Ошибка</AlertDialogTitle>
          <AlertDialogDescription>{message}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogAction className="col-span-2" onClick={onDismiss}>
            Понятно
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
