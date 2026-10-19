package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
)

// wmsABI — ContractABI (SignedMappingWMS), распарсенный один раз на пакет:
// NewWarehouseActions и PackApplyActions переиспользуют один и тот же
// экземпляр, embed не перечитывается.
var wmsABI = mustParseWMSABI()

func mustParseWMSABI() abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(ContractABI))
	if err != nil {
		panic(fmt.Sprintf("parse ContractABI: %v", err)) // embed проверяется тестами пакета
	}
	return parsed
}

// WarehouseAction — SignedMappingWMS.Action: одно подписанное складское
// действие (Receive/PutAway/Pick/Ship, ADR §1). UUID ↔ bytes16 — сырые 16 байт
// UUID; ItemIds — uint256(keccak256(uuid)) в порядке конверта (ADR §9: порядок
// проставляет WMS, адаптер не меняет). Имена полей — это компоненты ABI-tuple
// после abi.ToCamelCase.
type WarehouseAction struct {
	ActionId    [16]byte
	ActionType  uint8
	ItemIds     []*big.Int
	DetailsHash [32]byte
	KeyId       [32]byte
	Auth        WebAuthnAuth
}

// PackApplyActions кодирует calldata вызова SignedMappingWMS.applyActions.
func PackApplyActions(actions []WarehouseAction) ([]byte, error) {
	data, err := wmsABI.Pack("applyActions", actions)
	if err != nil {
		return nil, fmt.Errorf("pack applyActions: %w", err)
	}
	return data, nil
}

// WarehouseActions — read-only резолвер исходов SignedMappingWMS (логи
// receipt'ов tx с applyActions и actionState по actionId).
type WarehouseActions struct {
	actionContract
}

// NewWarehouseActions строит резолвер по адресу SignedMappingWMS.
func NewWarehouseActions(eth contractCaller, addr common.Address) *WarehouseActions {
	return &WarehouseActions{actionContract{eth: eth, addr: addr, abi: wmsABI}}
}

// LogOutcomes восстанавливает исход подписанных складских действий из логов
// receipt'а tx с applyActions. Логи контракта (Address == w.addr):
//   - ActionApplied (actionId — indexed bytes16 в topic1, выровнен влево)
//     → Applied;
//   - ActionRejected (actionId — indexed bytes16 в topic1; reason uint8 —
//     первое non-indexed поле в data) → Reason.
//
// actionId без лога в этом receipt'е (повторная отправка, ADR §6) резолвится
// StateOutcome'ом по actionState.
func (w *WarehouseActions) LogOutcomes(receipt *types.Receipt) (map[uuid.UUID]ActionOutcome, error) {
	actionApplied := w.abi.Events["ActionApplied"]
	actionRejected := w.abi.Events["ActionRejected"]

	outcomes := make(map[uuid.UUID]ActionOutcome)
	for _, l := range receipt.Logs {
		if l.Address != w.addr || len(l.Topics) == 0 {
			continue
		}
		switch l.Topics[0] {
		case actionApplied.ID:
			if len(l.Topics) < 2 {
				return nil, errors.New("ActionApplied: нет actionId в topics")
			}
			var id [16]byte
			copy(id[:], l.Topics[1][:16])
			outcomes[uuid.UUID(id)] = ActionOutcome{Applied: true}
		case actionRejected.ID:
			if len(l.Topics) < 2 {
				return nil, errors.New("ActionRejected: нет actionId в topics")
			}
			reason, err := unpackRejectReason(actionRejected, l.Data)
			if err != nil {
				return nil, err
			}
			var id [16]byte
			copy(id[:], l.Topics[1][:16])
			outcomes[uuid.UUID(id)] = ActionOutcome{Reason: reason}
		}
	}
	return outcomes, nil
}

// StateOutcome — исход одного actionId по actionState контракта (fallback для
// id без лога в receipt'е): Applied → Applied; Rejected → reason из
// ActionRejected-лога журнала; None → (zero, false, nil) — контракт действие
// не знает.
func (w *WarehouseActions) StateOutcome(ctx context.Context, id uuid.UUID) (ActionOutcome, bool, error) {
	return w.stateOutcome(ctx, id)
}
