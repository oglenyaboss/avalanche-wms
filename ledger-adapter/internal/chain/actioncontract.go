package chain

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
)

// SignedAction.State (ADR-0001 §1): исход подписанного действия на цепи.
const (
	actionStateApplied  uint8 = 1
	actionStateRejected uint8 = 2
)

// actionContract — общая plumbing read-only обёрток над контрактами с
// SignedAction-состоянием (ConsortiumRegistry и SignedMappingWMS): eth_call,
// чтение actionState и разбор reject-reason из журнала.
type actionContract struct {
	eth  contractCaller
	addr common.Address
	abi  abi.ABI
}

// Address — адрес контракта.
func (c *actionContract) Address() common.Address { return c.addr }

// call выполняет eth_call с calldata data на адрес контракта.
func (c *actionContract) call(ctx context.Context, data []byte) ([]byte, error) {
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &c.addr, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("пустой ответ eth_call — по адресу нет контракта")
	}
	return out, nil
}

// actionState читает SignedAction.State по actionId (0 None, 1 Applied, 2 Rejected).
func (c *actionContract) actionState(ctx context.Context, id [16]byte) (uint8, error) {
	data, err := c.abi.Pack("actionState", id)
	if err != nil {
		return 0, fmt.Errorf("pack actionState: %w", err)
	}
	out, err := c.call(ctx, data)
	if err != nil {
		return 0, fmt.Errorf("call actionState: %w", err)
	}
	vals, err := c.abi.Unpack("actionState", out)
	if err != nil {
		return 0, fmt.Errorf("unpack actionState: %w", err)
	}
	state, ok := vals[0].(uint8)
	if !ok {
		return 0, errors.New("actionState: значение не uint8")
	}
	return state, nil
}

// rejectedReason достаёт reason отклонённого actionId из журнала контракта:
// FilterLogs по ActionRejected с topic1 = actionId (повторы одного actionId
// на цепи невозможны — контракт применяет действие единожды), берём
// последний лог.
func (c *actionContract) rejectedReason(ctx context.Context, actionRejected abi.Event, id uuid.UUID) (uint8, error) {
	var topic common.Hash
	copy(topic[:], id[:]) // indexed bytes16 в topic выровнен влево

	logs, err := c.eth.FilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{c.addr},
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

// stateOutcome — исход одного actionId по actionState (fallback для id без
// лога в receipt'е): Applied → Applied; Rejected → reason из ActionRejected-
// лога журнала; None → (zero, false, nil) — контракт действие не знает.
func (c *actionContract) stateOutcome(ctx context.Context, id uuid.UUID) (ActionOutcome, bool, error) {
	state, err := c.actionState(ctx, [16]byte(id))
	if err != nil {
		return ActionOutcome{}, false, fmt.Errorf("action state %s: %w", id, err)
	}
	switch state {
	case actionStateApplied:
		return ActionOutcome{Applied: true}, true, nil
	case actionStateRejected: // reason достаём из журнала
		reason, err := c.rejectedReason(ctx, c.abi.Events["ActionRejected"], id)
		if err != nil {
			return ActionOutcome{}, false, err
		}
		return ActionOutcome{Reason: reason}, true, nil
	default: // SignedAction.State.None — исхода на цепи нет
		return ActionOutcome{}, false, nil
	}
}

// unpackRejectReason анпакит reason (первое non-indexed поле ActionRejected)
// из data лога.
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
