package audit

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedaction"
)

// ChainAction is one applied signed warehouse action as the chain logged it.
type ChainAction struct {
	ActionID    uuid.UUID
	KeyID       [32]byte
	OrgID       uuid.UUID
	ActionType  uint8
	DetailsHash [32]byte
	ItemCount   uint64
	BlockNumber uint64
	BlockTime   time.Time
	TxHash      common.Hash
}

// ChainRejection is the on-chain rejection of one signed warehouse action.
type ChainRejection struct {
	ActionID  uuid.UUID
	Reason    uint8
	ItemIndex uint64
	TxHash    common.Hash
}

// Key is a registered signer key as ConsortiumRegistry stores it.
type Key struct {
	Point  domain.P256Point
	OrgID  uuid.UUID
	Role   uint8
	Active bool
}

// Chain reads the chain side of signed warehouse actions from SignedMappingWMS and ConsortiumRegistry.
type Chain struct {
	client     *ethclient.Client
	wms        common.Address
	registry   common.Address
	blockTimes map[uint64]time.Time
}

// DialChain connects to the RPC endpoint and pins both contract addresses.
func DialChain(ctx context.Context, rpcURL string, wms, registry common.Address) (*Chain, error) {
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("audit.DialChain: %w", err)
	}
	return &Chain{client: client, wms: wms, registry: registry, blockTimes: make(map[uint64]time.Time)}, nil
}

// Close releases the RPC connection.
func (c *Chain) Close() {
	c.client.Close()
}

// ActionIDsForItem returns the unique action ids of the ItemMoved logs for the product, in log order.
func (c *Chain) ActionIDsForItem(ctx context.Context, productID uuid.UUID) ([]uuid.UUID, error) {
	itemMoved := wmsABI.Events["ItemMoved"]
	itemID := common.Hash(signedaction.ItemID(productID))
	logs, err := c.filterLogs(ctx, [][]common.Hash{{itemMoved.ID}, {itemID}})
	if err != nil {
		return nil, fmt.Errorf("audit.Chain.ActionIDsForItem: %w", err)
	}
	var out []uuid.UUID
	seen := make(map[uuid.UUID]bool)
	for _, l := range logs {
		if len(l.Topics) < 3 {
			continue
		}
		id := uuidFromTopic(l.Topics[2])
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// ItemsOfAction returns the item ids of the ItemMoved logs of the action, in log order.
func (c *Chain) ItemsOfAction(ctx context.Context, actionID uuid.UUID) ([][32]byte, error) {
	itemMoved := wmsABI.Events["ItemMoved"]
	logs, err := c.filterLogs(ctx, [][]common.Hash{{itemMoved.ID}, nil, {actionTopic(actionID)}})
	if err != nil {
		return nil, fmt.Errorf("audit.Chain.ItemsOfAction: %w", err)
	}
	var out [][32]byte
	for _, l := range logs {
		if len(l.Topics) < 2 {
			continue
		}
		out = append(out, [32]byte(l.Topics[1]))
	}
	return out, nil
}

// Applied maps the action id to the ActionApplied log, last log per id wins;
// a nil or empty id list matches every action.
func (c *Chain) Applied(ctx context.Context, actionIDs []uuid.UUID) (map[uuid.UUID]ChainAction, error) {
	applied := wmsABI.Events["ActionApplied"]
	var topic1 []common.Hash
	for _, id := range actionIDs {
		topic1 = append(topic1, actionTopic(id))
	}
	logs, err := c.filterLogs(ctx, [][]common.Hash{{applied.ID}, topic1})
	if err != nil {
		return nil, fmt.Errorf("audit.Chain.Applied: %w", err)
	}
	out := make(map[uuid.UUID]ChainAction)
	for _, l := range logs {
		if len(l.Topics) < 4 {
			continue
		}
		actionType, detailsHash, itemCount, err := unpackApplied(applied, l.Data)
		if err != nil {
			return nil, fmt.Errorf("audit.Chain.Applied: %w", err)
		}
		blockTime, err := c.blockTime(ctx, l.BlockNumber)
		if err != nil {
			return nil, fmt.Errorf("audit.Chain.Applied: %w", err)
		}
		id := uuidFromTopic(l.Topics[1])
		out[id] = ChainAction{
			ActionID:    id,
			KeyID:       [32]byte(l.Topics[2]),
			OrgID:       uuidFromTopic(l.Topics[3]),
			ActionType:  actionType,
			DetailsHash: detailsHash,
			ItemCount:   itemCount,
			BlockNumber: l.BlockNumber,
			BlockTime:   blockTime,
			TxHash:      l.TxHash,
		}
	}
	return out, nil
}

// Rejected maps the action id to its last ActionRejected log, last log per id wins;
// a nil or empty id list matches every action.
func (c *Chain) Rejected(ctx context.Context, actionIDs []uuid.UUID) (map[uuid.UUID]ChainRejection, error) {
	rejected := wmsABI.Events["ActionRejected"]
	var topic1 []common.Hash
	for _, id := range actionIDs {
		topic1 = append(topic1, actionTopic(id))
	}
	logs, err := c.filterLogs(ctx, [][]common.Hash{{rejected.ID}, topic1})
	if err != nil {
		return nil, fmt.Errorf("audit.Chain.Rejected: %w", err)
	}
	out := make(map[uuid.UUID]ChainRejection)
	for _, l := range logs {
		if len(l.Topics) < 2 {
			continue
		}
		reason, itemIndex, err := unpackRejected(rejected, l.Data)
		if err != nil {
			return nil, fmt.Errorf("audit.Chain.Rejected: %w", err)
		}
		id := uuidFromTopic(l.Topics[1])
		out[id] = ChainRejection{
			ActionID:  id,
			Reason:    reason,
			ItemIndex: itemIndex,
			TxHash:    l.TxHash,
		}
	}
	return out, nil
}

// Key reads the registered signer key by its keyId.
func (c *Chain) Key(ctx context.Context, keyID [32]byte) (Key, error) {
	data, err := registryABI.Pack("getKey", keyID)
	if err != nil {
		return Key{}, fmt.Errorf("audit.Chain.Key pack: %w", err)
	}
	out, err := c.client.CallContract(ctx, ethereum.CallMsg{To: &c.registry, Data: data}, nil)
	if err != nil {
		return Key{}, fmt.Errorf("audit.Chain.Key call: %w", err)
	}
	if len(out) == 0 {
		return Key{}, errors.New("audit.Chain.Key: empty eth_call response")
	}
	res, err := registryABI.Unpack("getKey", out)
	if err != nil {
		return Key{}, fmt.Errorf("audit.Chain.Key unpack: %w", err)
	}
	t := abi.ConvertType(res[0], new(keyTuple)).(*keyTuple)
	return Key{Point: domain.P256Point{X: t.Qx, Y: t.Qy}, OrgID: uuid.UUID(t.OrgId), Role: t.Role, Active: t.Active}, nil
}

// keyTuple mirrors ConsortiumRegistry.Key: go-ethereum maps tuple components to fields by name.
type keyTuple struct {
	Qx     [32]byte
	Qy     [32]byte
	OrgId  [16]byte
	Role   uint8
	Active bool
}

func (c *Chain) filterLogs(ctx context.Context, topics [][]common.Hash) ([]types.Log, error) {
	// ponytail: один FilterLogs на весь диапазон; на длинной цепи разбить по блокам
	logs, err := c.client.FilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{c.wms},
		Topics:    topics,
	})
	if err != nil {
		return nil, fmt.Errorf("audit.Chain filterLogs: %w", err)
	}
	return logs, nil
}

func (c *Chain) blockTime(ctx context.Context, number uint64) (time.Time, error) {
	if t, ok := c.blockTimes[number]; ok {
		return t, nil
	}
	header, err := c.client.HeaderByNumber(ctx, new(big.Int).SetUint64(number))
	if err != nil {
		return time.Time{}, fmt.Errorf("audit.Chain.blockTime %d: %w", number, err)
	}
	t := time.Unix(int64(header.Time), 0)
	c.blockTimes[number] = t
	return t, nil
}

// actionTopic is the topic of an indexed bytes16 actionId: the 16 UUID bytes left-aligned.
func actionTopic(id uuid.UUID) common.Hash {
	var h common.Hash
	copy(h[:16], id[:])
	return h
}

func uuidFromTopic(topic common.Hash) uuid.UUID {
	var id [16]byte
	copy(id[:], topic[:16])
	return uuid.UUID(id)
}

// unpackApplied unpacks the non-indexed data of ActionApplied: actionType, detailsHash, itemCount.
func unpackApplied(event abi.Event, data []byte) (uint8, [32]byte, uint64, error) {
	vals, err := event.Inputs.NonIndexed().Unpack(data)
	if err != nil {
		return 0, [32]byte{}, 0, err
	}
	actionType, ok := vals[0].(uint8)
	if !ok {
		return 0, [32]byte{}, 0, errors.New("actionType is not uint8")
	}
	detailsHash, ok := vals[1].([32]byte)
	if !ok {
		return 0, [32]byte{}, 0, errors.New("detailsHash is not bytes32")
	}
	itemCount, ok := vals[2].(*big.Int)
	if !ok {
		return 0, [32]byte{}, 0, errors.New("itemCount is not uint256")
	}
	return actionType, detailsHash, itemCount.Uint64(), nil
}

// unpackRejected unpacks the non-indexed data of ActionRejected: reason, itemIndex.
func unpackRejected(event abi.Event, data []byte) (uint8, uint64, error) {
	vals, err := event.Inputs.NonIndexed().Unpack(data)
	if err != nil {
		return 0, 0, err
	}
	reason, ok := vals[0].(uint8)
	if !ok {
		return 0, 0, errors.New("reason is not uint8")
	}
	itemIndex, ok := vals[1].(*big.Int)
	if !ok {
		return 0, 0, errors.New("itemIndex is not uint256")
	}
	return reason, itemIndex.Uint64(), nil
}
