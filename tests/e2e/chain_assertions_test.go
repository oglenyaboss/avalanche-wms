//go:build e2e

package e2e

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const itemStatusABI = `[{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"itemStatus","outputs":[{"internalType":"enum BatchMappingWMS.Status","name":"","type":"uint8"}],"stateMutability":"view","type":"function"}]`

// fsmStageOrder is the canonical FSM order the contract enforces for an item.
var fsmStageOrder = []string{"receiving", "putaway", "picking", "shipping"}

// callItemStatus reads the on-chain itemStatus enum for a product id.
func callItemStatus(t *testing.T, ctx context.Context, env *env, productID uuid.UUID) uint8 {
	t.Helper()

	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()

	parsedABI, err := abi.JSON(strings.NewReader(itemStatusABI))
	require.NoError(t, err)

	itemID := uuidToUint256(productID.String())
	data, err := parsedABI.Pack("itemStatus", itemID)
	require.NoError(t, err)

	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   ptr(common.HexToAddress(env.contractAddr)),
		Data: data,
	}, nil)
	require.NoError(t, err)

	values, err := parsedABI.Unpack("itemStatus", result)
	require.NoError(t, err)
	require.Len(t, values, 1)

	status, ok := values[0].(uint8)
	require.Truef(t, ok, "unexpected itemStatus type %T", values[0])
	return status
}

// uuidToUint256 maps a UUID string to the uint256 item id the contract uses
// (keccak256 of the string bytes).
func uuidToUint256(s string) *big.Int {
	return new(big.Int).SetBytes(crypto.Keccak256([]byte(s)))
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T {
	return &v
}

// txPosition is a transaction's position on chain: (blockNumber, txIndex).
// It establishes a total order over confirmed transactions.
type txPosition struct {
	block    *big.Int
	txIndex  uint
	hasValue bool
}

// before reports whether p strictly precedes other in chain order.
func (p txPosition) before(other txPosition) bool {
	if c := p.block.Cmp(other.block); c != 0 {
		return c < 0
	}
	return p.txIndex < other.txIndex
}

// requireOnchainFSMOrdering verifies that per-stage tx position maxima are
// non-decreasing across the FSM order: max(receiving) <= max(putaway) <=
// max(picking) <= max(shipping). This catches gross ordering violations (e.g.
// all shipping txs mined before any receiving tx) but allows per-item
// interleaving within a stage — per-item ordering is enforced by the contract's
// _requireNewEvent guard, not by this assertion.
func requireOnchainFSMOrdering(t *testing.T, ctx context.Context, env *env, stageEventIDs map[string][]uuid.UUID) {
	t.Helper()

	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()

	var prevStage string
	var prevMax txPosition
	for _, stage := range fsmStageOrder {
		eventIDs := stageEventIDs[stage]
		if len(eventIDs) == 0 {
			continue
		}

		var stageMax txPosition
		for _, eventID := range eventIDs {
			var status, txHash string
			err := env.db.QueryRow(ctx, `
				SELECT status::text, COALESCE(tx_hash, '')
				FROM public.onchain_events
				WHERE event_id = $1`, eventID).Scan(&status, &txHash)
			require.NoError(t, err, "onchain event %s (%s)", eventID, stage)
			require.Equalf(t, "COMMITTED", status, "event %s (%s) not COMMITTED", eventID, stage)
			require.NotEmptyf(t, txHash, "event %s (%s) has empty tx_hash", eventID, stage)

			receipt, err := client.TransactionReceipt(ctx, common.HexToHash(txHash))
			require.NoErrorf(t, err, "receipt for tx %s (%s)", txHash, stage)

			pos := txPosition{
				block:    receipt.BlockNumber,
				txIndex:  receipt.TransactionIndex,
				hasValue: true,
			}
			if !stageMax.hasValue || stageMax.before(pos) {
				stageMax = pos
			}
		}

		if prevMax.hasValue {
			require.Falsef(t, stageMax.before(prevMax),
				"FSM ordering violated: stage %q max tx position (%s,%d) precedes stage %q max (%s,%d)",
				stage, stageMax.block, stageMax.txIndex, prevStage, prevMax.block, prevMax.txIndex)
		}
		prevStage = stage
		prevMax = stageMax
	}
}
