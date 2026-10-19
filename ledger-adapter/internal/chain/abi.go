package chain

import _ "embed"

// ContractABI — ABI SignedMappingWMS (контракт v2: старые batch-функции и applyActions),
// сгенерирован через `forge build` +
// `jq -c '.abi' out/SignedMappingWMS.sol/SignedMappingWMS.json`.
// Обновлять при изменении контракта.
//
//go:embed SignedMappingWMS.abi.json
var ContractABI string

// RegistryABI — ABI ConsortiumRegistry, сгенерирован так же:
// `jq -c '.abi' out/ConsortiumRegistry.sol/ConsortiumRegistry.json`.
//
//go:embed ConsortiumRegistry.abi.json
var RegistryABI string
