package chain

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// WebAuthnAuth — WebAuthn.WebAuthnAuth из контракта. Имена полей — это
// компоненты ABI-tuple после abi.ToCamelCase (r→R, s→S, challengeIndex→
// ChallengeIndex, …): go-ethereum пакует и анпакивает tuple в Go-структуру
// по этим именам.
type WebAuthnAuth struct {
	R, S              [32]byte
	ChallengeIndex    *big.Int
	TypeIndex         *big.Int
	AuthenticatorData []byte
	ClientDataJSON    string
}

// KeyAction — ConsortiumRegistry.KeyAction: одно подписанное действие
// (KeyRegister/KeyRevoke, ADR §2). UUID ↔ bytes16 — сырые 16 байт UUID.
type KeyAction struct {
	ActionId    [16]byte
	ActionType  uint8
	Qx, Qy      [32]byte
	SignerKeyId [32]byte
	Auth        WebAuthnAuth
}

// ActionOutcome — исход одного actionId (из receipt'а или actionState):
// применён, либо отклонён контрактом. Reason — код ADR §6 RejectReason;
// > 0 только при reject.
type ActionOutcome struct {
	Applied bool
	Reason  uint8
}

// registryABI — RegistryABI, распарсенный один раз на пакет: NewRegistry и
// PackApplyKeyActions переиспользуют один и тот же экземпляр, embed не
// перечитывается.
var registryABI = mustParseRegistryABI()

func mustParseRegistryABI() abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(RegistryABI))
	if err != nil {
		panic(fmt.Sprintf("parse RegistryABI: %v", err)) // embed проверяется тестами пакета
	}
	return parsed
}

// PackApplyKeyActions кодирует calldata вызова ConsortiumRegistry.applyKeyActions.
func PackApplyKeyActions(actions []KeyAction) ([]byte, error) {
	data, err := registryABI.Pack("applyKeyActions", actions)
	if err != nil {
		return nil, fmt.Errorf("pack applyKeyActions: %w", err)
	}
	return data, nil
}
