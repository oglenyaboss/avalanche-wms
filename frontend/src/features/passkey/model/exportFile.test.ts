import { describe, expect, it } from 'vitest'

import { adminKeyFileContent, adminKeyFileName } from './exportFile'

const data = { orgId: 'org-uuid', name: 'Acme', qx: '0x01', qy: '0x02' }

describe('adminKeyFileName', () => {
  it('builds org-<orgId>.json', () => {
    expect(adminKeyFileName('org-uuid')).toBe('org-org-uuid.json')
  })
})

describe('adminKeyFileContent', () => {
  it('serializes exactly orgId, name, qx, qy with trailing newline', () => {
    const content = adminKeyFileContent(data)
    expect(content).toBe(
      '{\n  "orgId": "org-uuid",\n  "name": "Acme",\n  "qx": "0x01",\n  "qy": "0x02"\n}\n'
    )
    expect(Object.keys(JSON.parse(content))).toEqual(['orgId', 'name', 'qx', 'qy'])
  })
})
