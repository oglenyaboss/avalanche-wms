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
	eth  contractCaller
	addr common.Address
	abi  abi.ABI
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
	return &Registry{eth: eth, addr: addr, abi: registryABI}, nil
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

// Address — адрес контракта реестра.
func (r *Registry) Address() common.Address { return r.addr }

// ActionOutcomes восстанавливает исход каждого actionId из receipt'а tx с
// applyKeyActions. Логи реестра (Address == r.addr):
//   - KeyRegistered/KeyRevoked (actionId — последнее non-indexed поле в data)
//     → Applied;
//   - ActionRejected (actionId — indexed bytes16 в topic1, выровнен влево;
//     reason uint8 в data) → Reason.
//
// Для actionId без лога в receipt'е (повторная отправка, ADR §6) читается
// actionState: 1 (Applied) → Applied; 2 (Rejected) → reason из ActionRejected-
// лога по topic1; 0 (None) — исхода на цепи нет, ошибка.
func (r *Registry) ActionOutcomes(ctx context.Context, receipt *types.Receipt, ids []uuid.UUID) (map[uuid.UUID]ActionOutcome, error) {
	keyRegistered := r.abi.Events["KeyRegistered"]
	keyRevoked := r.abi.Events["KeyRevoked"]
	actionRejected := r.abi.Events["ActionRejected"]

	outcomes := make(map[uuid.UUID]ActionOutcome, len(ids))
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

	for _, id := range ids {
		if _, ok := outcomes[id]; ok {
			continue
		}
		state, err := r.actionState(ctx, [16]byte(id))
		if err != nil {
			return nil, err
		}
		switch state {
		case 1: // SignedAction.State.Applied
			outcomes[id] = ActionOutcome{Applied: true}
		case 2: // SignedAction.State.Rejected — reason достаём из журнала
			reason, err := r.rejectedReason(ctx, actionRejected, id)
			if err != nil {
				return nil, err
			}
			outcomes[id] = ActionOutcome{Reason: reason}
		default: // SignedAction.State.None — исхода на цепи нет
			return nil, fmt.Errorf("no outcome for action %s", id)
		}
	}
	return outcomes, nil
}

// actionState читает SignedAction.State по actionId (0 None, 1 Applied, 2 Rejected).
func (r *Registry) actionState(ctx context.Context, id [16]byte) (uint8, error) {
	data, err := r.abi.Pack("actionState", id)
	if err != nil {
		return 0, fmt.Errorf("pack actionState: %w", err)
	}
	out, err := r.call(ctx, data)
	if err != nil {
		return 0, err
	}
	vals, err := r.abi.Unpack("actionState", out)
	if err != nil {
		return 0, fmt.Errorf("unpack actionState: %w", err)
	}
	state, ok := vals[0].(uint8)
	if !ok {
		return 0, errors.New("actionState: значение не uint8")
	}
	return state, nil
}

// rejectedReason достаёт reason отклонённого actionId из журнала реестра:
// FilterLogs по ActionRejected с topic1 = actionId (повторы одного actionId
// на цепи невозможны — контракт применяет действие единожды), берём
// последний лог.
func (r *Registry) rejectedReason(ctx context.Context, actionRejected abi.Event, id uuid.UUID) (uint8, error) {
	var topic common.Hash
	copy(topic[:], id[:]) // indexed bytes16 в topic выровнен влево

	logs, err := r.eth.FilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{r.addr},
		Topics:    [][]common.Hash{{actionRejected.ID}, {topic}},
	})
	if err != nil {
		return 0, fmt.Errorf("filter ActionRejected %s: %w", id, err)
	}
	if len(logs) == 0 {
		return 0, fmt.Errorf("no ActionRejected log for action %s", id)
	}
	return unpackRejectReason(actionRejected, logs[len(logs)-1].Data)
}

// unpackRejectReason анпакит reason (единственное non-indexed поле
// ActionRejected) из data лога.
func unpackRejectReason(actionRejected abi.Event, data []byte) (uint8, error) {
	vals, err := actionRejected.Inputs.NonIndexed().Unpack(data)
	if err != nil {
		return 0, fmt.Errorf("unpack ActionRejected: %w", err)
	}
	reason, ok := vals[0].(uint8)
	if !ok {
		return 0, errors.New("ActionRejected: reason не uint8")
	}
	return reason, nil
}

func (r *Registry) call(ctx context.Context, data []byte) ([]byte, error) {
	out, err := r.eth.CallContract(ctx, ethereum.CallMsg{To: &r.addr, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("registry: пустой ответ, по адресу нет контракта")
	}
	return out, nil
}
