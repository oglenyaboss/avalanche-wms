// Auditor settings for the direct chain read: persisted via useAuditSettings.
import { isAddress } from 'viem'

import { Card, CardContent, Field, FieldLabel, Input } from '@/shared/ui'

import { useAuditSettings } from '../model/useAuditSettings'

export function AuditSettings() {
  const { rpcUrl, contractAddress, setRpcUrl, setContractAddress } = useAuditSettings()
  const addressInvalid = contractAddress !== '' && !isAddress(contractAddress)

  return (
    <Card>
      <CardContent className="flex flex-col gap-4">
        <Field>
          <FieldLabel htmlFor="audit-rpc">RPC-узел</FieldLabel>
          <Input
            id="audit-rpc"
            value={rpcUrl}
            onChange={(e) => setRpcUrl(e.target.value)}
            placeholder="http://localhost:9699/ext/bc/C/rpc"
            autoComplete="off"
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="audit-contract">Адрес контракта SignedMappingWMS</FieldLabel>
          <Input
            id="audit-contract"
            value={contractAddress}
            onChange={(e) => setContractAddress(e.target.value)}
            placeholder="0x…"
            autoComplete="off"
            aria-invalid={addressInvalid}
            className="font-mono"
          />
          {addressInvalid ? (
            <p className="text-xs text-status-failed">Адрес контракта некорректен</p>
          ) : null}
        </Field>
      </CardContent>
    </Card>
  )
}
