// Human-readable ABIs for the audit reads; signatures cross-checked against
// SignedMappingWMS.abi.json and ConsortiumRegistry.abi.json.
import { getAbiItem, parseAbi } from 'viem'

export const wmsAbi = parseAbi([
  'event ItemMoved(uint256 indexed itemId, bytes16 indexed actionId, uint8 status)',
  'event ActionApplied(bytes16 indexed actionId, bytes32 indexed keyId, bytes16 indexed orgId, uint8 actionType, bytes32 detailsHash, uint256 itemCount)',
  'event ActionRejected(bytes16 indexed actionId, uint8 reason, uint256 itemIndex)',
  'function registry() view returns (address)',
  'function applyActions((bytes16 actionId, uint8 actionType, uint256[] itemIds, bytes32 detailsHash, bytes32 keyId, (bytes32 r, bytes32 s, uint256 challengeIndex, uint256 typeIndex, bytes authenticatorData, string clientDataJSON) auth)[] actions)',
])

export const registryAbi = parseAbi([
  'function getKey(bytes32 keyId) view returns ((bytes32 qx, bytes32 qy, bytes16 orgId, uint8 role, bool active))',
  'event OrganizationRegistered(bytes16 indexed orgId, string name)',
])

export const itemMovedEvent = getAbiItem({ abi: wmsAbi, name: 'ItemMoved' })
export const actionAppliedEvent = getAbiItem({ abi: wmsAbi, name: 'ActionApplied' })
export const actionRejectedEvent = getAbiItem({ abi: wmsAbi, name: 'ActionRejected' })
export const organizationRegisteredEvent = getAbiItem({ abi: registryAbi, name: 'OrganizationRegistered' })
