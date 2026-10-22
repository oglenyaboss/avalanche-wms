import { HugeiconsIcon } from '@hugeicons/react'
import { Download04Icon } from '@hugeicons/core-free-icons'

import { Alert, AlertDescription, Button } from '@/shared/ui'

import { useAdminKeyDownload } from '../model/usePasskey'

export function AdminKeyDownloadButton() {
  const { download, isPending, errorText } = useAdminKeyDownload()

  return (
    <div className="flex flex-col gap-3">
      {errorText && (
        <Alert variant="destructive">
          <AlertDescription>{errorText}</AlertDescription>
        </Alert>
      )}
      <Button variant="outline" className="self-start" disabled={isPending} onClick={() => download()}>
        <HugeiconsIcon icon={Download04Icon} strokeWidth={1.8} />
        {isPending ? 'Загрузка…' : 'Скачать ключ админа'}
      </Button>
    </div>
  )
}
