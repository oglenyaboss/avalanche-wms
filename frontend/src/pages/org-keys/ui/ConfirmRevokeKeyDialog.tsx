import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/shared/ui'

interface ConfirmRevokeKeyDialogProps {
  open: boolean
  onConfirm: () => void
  onCancel: () => void
}

// Confirms the admin wants to revoke an active operator key; the signing
// itself starts only after the dialog is accepted.
export function ConfirmRevokeKeyDialog({ open, onConfirm, onCancel }: ConfirmRevokeKeyDialogProps) {
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
          <AlertDialogTitle>Отозвать ключ?</AlertDialogTitle>
          <AlertDialogDescription>
            Ключ оператора перестанет действовать в цепи. Подтвердите отзыв своим ключом.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Отмена</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={onConfirm}>
            Отозвать
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
