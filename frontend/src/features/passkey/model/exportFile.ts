import type { AdminKeyExport } from './types'

export function adminKeyFileName(orgId: string): string {
  return `org-${orgId}.json`
}

export function adminKeyFileContent(data: AdminKeyExport): string {
  const { orgId, name, qx, qy } = data
  return JSON.stringify({ orgId, name, qx, qy }, null, 2) + '\n'
}

export function downloadAdminKey(data: AdminKeyExport): void {
  const url = URL.createObjectURL(
    new Blob([adminKeyFileContent(data)], { type: 'application/json' })
  )
  const link = document.createElement('a')
  link.href = url
  link.download = adminKeyFileName(data.orgId)
  link.click()
  URL.revokeObjectURL(url)
}
