package audit

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed SignedMappingWMS.abi.json
var wmsABIJSON string

//go:embed ConsortiumRegistry.abi.json
var registryABIJSON string

var (
	wmsABI      = mustParseABI(wmsABIJSON)
	registryABI = mustParseABI(registryABIJSON)
)

func mustParseABI(json string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(json))
	if err != nil {
		panic(fmt.Sprintf("audit: parse embedded ABI: %v", err)) // embed константа, проверяется тестами пакета
	}
	return parsed
}
