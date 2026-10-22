package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
)

const (
	RoleNone     uint8 = 0
	RoleAdmin    uint8 = 1
	RoleOperator uint8 = 2
)

// RegistryKey — ключ из ConsortiumRegistry.
type RegistryKey struct {
	QX, QY [32]byte
	OrgID  [16]byte
	Role   uint8
	Active bool
}

// contractCaller — подмножество ethclient для eth_call и чтения логов.
type contractCaller interface {
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
}

// Registry — read-only обёртка над ConsortiumRegistry.
type Registry struct {
	actionContract
}

// registryKeyTuple совпадает с Key из контракта: go-ethereum сопоставляет
// компоненты tuple по именам полей.
type registryKeyTuple struct {
	Qx     [32]byte
	Qy     [32]byte
	OrgId  [16]byte
	Role   uint8
	Active bool
}

func NewRegistry(eth contractCaller, addr common.Address) (*Registry, error) {
	return &Registry{actionContract{eth: eth, addr: addr, abi: registryABI}}, nil
}

func (r *Registry) ABI() abi.ABI { return r.abi }

func (r *Registry) GetKey(ctx context.Context, keyID [32]byte) (RegistryKey, error) {
	data, err := r.abi.Pack("getKey", keyID)
	if err != nil {
		return RegistryKey{}, err
	}
	out, err := r.call(ctx, data)
	if err != nil {
		return RegistryKey{}, err
	}
	res, err := r.abi.Unpack("getKey", out)
	if err != nil {
		return RegistryKey{}, err
	}
	t := abi.ConvertType(res[0], new(registryKeyTuple)).(*registryKeyTuple)
	return RegistryKey{QX: t.Qx, QY: t.Qy, OrgID: t.OrgId, Role: t.Role, Active: t.Active}, nil
}

func (r *Registry) OrganizationExists(ctx context.Context, orgID [16]byte) (bool, error) {
	data, err := r.abi.Pack("organizationExists", orgID)
	if err != nil {
		return false, err
	}
	out, err := r.call(ctx, data)
	if err != nil {
		return false, err
	}
	res, err := r.abi.Unpack("organizationExists", out)
	if err != nil {
		return false, err
	}
	return res[0].(bool), nil
}

// LogOutcomes восстанавливает исход подписанных действий из логов receipt'а tx с
// applyKeyActions. Логи реестра (Address == r.addr):
//   - KeyRegistered/KeyRevoked (actionId — последнее non-indexed поле в data)
//     → Applied;
//   - ActionRejected (actionId — indexed bytes16 в topic1, выровнен влево;
//     reason uint8 в data) → Reason.
//
// actionId без лога в этом receipt'е (повторная отправка, ADR §6) резолвится
// StateOutcome'ом по actionState.
func (r *Registry) LogOutcomes(receipt *types.Receipt) (map[uuid.UUID]ActionOutcome, error) {
	keyRegistered := r.abi.Events["KeyRegistered"]
	keyRevoked := r.abi.Events["KeyRevoked"]
	actionRejected := r.abi.Events["ActionRejected"]

	outcomes := make(map[uuid.UUID]ActionOutcome)
	for _, l := range receipt.Logs {
		if l.Address != r.addr || len(l.Topics) == 0 {
			continue
		}
		switch l.Topics[0] {
		case keyRegistered.ID, keyRevoked.ID:
			name := "KeyRevoked"
			if l.Topics[0] == keyRegistered.ID {
				name = "KeyRegistered"
			}
			vals, err := r.abi.Unpack(name, l.Data)
			if err != nil {
				return nil, fmt.Errorf("unpack %s: %w", name, err)
			}
			id, ok := vals[len(vals)-1].([16]byte)
			if !ok {
				return nil, fmt.Errorf("%s: actionId не bytes16", name)
			}
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

// StateOutcome — исход одного actionId по actionState реестра (fallback для id
// без лога в receipt'е): Applied → Applied; Rejected → reason из
// ActionRejected-лога журнала; None → (zero, false, nil) — исхода на цепи нет.
func (r *Registry) StateOutcome(ctx context.Context, id uuid.UUID) (ActionOutcome, bool, error) {
	return r.stateOutcome(ctx, id)
}
