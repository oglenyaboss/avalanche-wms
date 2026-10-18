package chain

import _ "embed"

// ContractABI — ABI BatchMappingWMS, сгенерирован через `forge build` +
// `jq -c '.abi' out/BatchMappingWMS.sol/BatchMappingWMS.json`.
// Обновлять при изменении контракта.
//
//go:embed BatchMappingWMS.abi.json
var ContractABI string

// RegistryABI — ABI ConsortiumRegistry, сгенерирован так же:
// `jq -c '.abi' out/ConsortiumRegistry.sol/ConsortiumRegistry.json`.
//
//go:embed ConsortiumRegistry.abi.json
var RegistryABI string
