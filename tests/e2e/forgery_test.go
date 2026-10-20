//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/asn1"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── direct applyActions packing (around the backend, owner key) ──────────────

var (
	wmsABITestOnce sync.Once
	wmsABITest     abi.ABI
)

// mustWMSABI parses the SignedMappingWMS ABI from the ledger-adapter tree once;
// a missing or broken ABI file is an environment failure, hence the panic.
func mustWMSABI() abi.ABI {
	wmsABITestOnce.Do(func() {
		raw, err := os.ReadFile(filepath.Join(testEnv.repoRoot, "ledger-adapter/internal/chain/SignedMappingWMS.abi.json"))
		if err != nil {
			panic(fmt.Errorf("read SignedMappingWMS ABI: %w", err))
		}
		parsed, err := abi.JSON(strings.NewReader(string(raw)))
		if err != nil {
			panic(fmt.Errorf("parse SignedMappingWMS ABI: %w", err))
		}
		wmsABITest = parsed
	})
	return wmsABITest
}

// wmsABIForTest returns the parsed ABI, reporting a parse failure through t.
func wmsABIForTest(t *testing.T) abi.ABI {
	t.Helper()
	return mustWMSABI()
}

// directAuth mirrors WebAuthn.WebAuthnAuth; field names must match the
// abi.ToCamelCase forms of the tuple components (r→R, challengeIndex→ChallengeIndex, …).
type directAuth struct {
	R, S              [32]byte
	ChallengeIndex    *big.Int
	TypeIndex         *big.Int
	AuthenticatorData []byte
	ClientDataJSON    string
}

// directAction mirrors SignedMappingWMS.Action the same way.
type directAction struct {
	ActionId    [16]byte
	ActionType  uint8
	ItemIds     []*big.Int
	DetailsHash [32]byte
	KeyId       [32]byte
	Auth        directAuth
}

// signDirectAction signs a fresh action over the sorted items (ADR-0001 §3)
// with the software signer, low-s normalized, indexes found by substring.
func signDirectAction(t *testing.T, signer *ecdsa.PrivateKey, keyID [32]byte, actionType uint8, items []uuid.UUID, detailsHash [32]byte) directAction {
	t.Helper()
	actionID := uuid.New()
	sorted := make([]uuid.UUID, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		return uuidToUint256(sorted[i].String()).Cmp(uuidToUint256(sorted[j].String())) < 0
	})
	challenge := softChallenge(actionID, actionType, softItemsHash(sorted), detailsHash)
	a := softSign(signer, challenge)

	cdj := string(a.ClientDataJSON)
	typeIndex := strings.Index(cdj, `"type":"webauthn.get"`)
	challengeIndex := strings.Index(cdj, `"challenge":"`)
	require.GreaterOrEqual(t, typeIndex, 0, "clientDataJSON lacks type")
	require.GreaterOrEqual(t, challengeIndex, 0, "clientDataJSON lacks challenge")

	var sig struct{ R, S *big.Int }
	_, err := asn1.Unmarshal(a.Signature, &sig)
	require.NoError(t, err)
	s := sig.S
	if s.Cmp(new(big.Int).Rsh(signer.Curve.Params().N, 1)) > 0 {
		s = new(big.Int).Sub(signer.Curve.Params().N, s)
	}

	itemIDs := make([]*big.Int, 0, len(sorted))
	for _, id := range sorted {
		itemIDs = append(itemIDs, uuidToUint256(id.String()))
	}
	return directAction{
		ActionId:    [16]byte(actionID),
		ActionType:  actionType,
		ItemIds:     itemIDs,
		DetailsHash: detailsHash,
		KeyId:       keyID,
		Auth: directAuth{
			R:                 word32(sig.R),
			S:                 word32(s),
			ChallengeIndex:    big.NewInt(int64(challengeIndex)),
			TypeIndex:         big.NewInt(int64(typeIndex)),
			AuthenticatorData: a.AuthenticatorData,
			ClientDataJSON:    cdj,
		},
	}
}

// word32 left-pads a big.Int into a 32-byte ABI word.
func word32(v *big.Int) [32]byte {
	var out [32]byte
	v.FillBytes(out[:])
	return out
}

// sendApplyActions packs the actions into applyActions calldata, sends it with
// the owner key straight to SignedMappingWMS (around the backend) and returns
// the receipt of the successful transaction.
func sendApplyActions(t *testing.T, ctx context.Context, env *env, actions ...directAction) *types.Receipt {
	t.Helper()
	data, err := wmsABIForTest(t).Pack("applyActions", actions)
	require.NoError(t, err, "pack applyActions")
	txHash := sendOwnerRegistryTx(t, ctx, env, ownerKeyForTest(t, ctx), common.HexToAddress(env.contractAddr), data)

	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()
	receipt, err := client.TransactionReceipt(ctx, txHash)
	require.NoError(t, err)
	return receipt
}

// actionTopicForTest is the topic of an indexed bytes16 actionId:
// the 16 UUID bytes left-aligned, zero tail.
func actionTopicForTest(id uuid.UUID) common.Hash {
	var h common.Hash
	copy(h[:16], id[:])
	return h
}

// rejectedReason finds the ActionRejected log of the action in the receipt.
func rejectedReason(t *testing.T, receipt *types.Receipt, actionID uuid.UUID) (uint8, bool) {
	t.Helper()
	event := wmsABIForTest(t).Events["ActionRejected"]
	wantTopic := actionTopicForTest(actionID)
	for _, l := range receipt.Logs {
		if len(l.Topics) < 2 || l.Topics[0] != event.ID || l.Topics[1] != wantTopic {
			continue
		}
		vals, err := event.Inputs.NonIndexed().Unpack(l.Data)
		require.NoError(t, err)
		reason, ok := vals[0].(uint8)
		require.Truef(t, ok, "reject reason type %T", vals[0])
		return reason, true
	}
	return 0, false
}

// appliedIn reports whether the receipt carries the ActionApplied log of the action.
func appliedIn(receipt *types.Receipt, actionID uuid.UUID) bool {
	event := mustWMSABI().Events["ActionApplied"]
	wantTopic := actionTopicForTest(actionID)
	for _, l := range receipt.Logs {
		if len(l.Topics) >= 2 && l.Topics[0] == event.ID && l.Topics[1] == wantTopic {
			return true
		}
	}
	return false
}

// itemMovedIn reports whether the receipt carries an ItemMoved log of the action.
func itemMovedIn(receipt *types.Receipt, actionID uuid.UUID) bool {
	event := mustWMSABI().Events["ItemMoved"]
	wantTopic := actionTopicForTest(actionID)
	for _, l := range receipt.Logs {
		if len(l.Topics) >= 3 && l.Topics[0] == event.ID && l.Topics[2] == wantTopic {
			return true
		}
	}
	return false
}

// callActionState reads SignedAction.State of the action (0 None, 1 Applied, 2 Rejected).
func callActionState(t *testing.T, ctx context.Context, env *env, actionID uuid.UUID) uint8 {
	t.Helper()
	data, err := wmsABIForTest(t).Pack("actionState", [16]byte(actionID))
	require.NoError(t, err)

	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()
	out, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   ptr(common.HexToAddress(env.contractAddr)),
		Data: data,
	}, nil)
	require.NoError(t, err)
	vals, err := wmsABIForTest(t).Unpack("actionState", out)
	require.NoError(t, err)
	require.Len(t, vals, 1)
	state, ok := vals[0].(uint8)
	require.Truef(t, ok, "actionState type %T", vals[0])
	return state
}

// freshItems returns n fresh product uuids (on-chain status None).
func freshItems(n int) []uuid.UUID {
	items := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, uuid.New())
	}
	return items
}

// freshDetailsHash returns a random details hash, as the backend would store.
func freshDetailsHash(t *testing.T) [32]byte {
	t.Helper()
	var h [32]byte
	_, err := rand.Read(h[:])
	require.NoError(t, err)
	return h
}

// ── forgery tests: forged calls straight to the contract, no backend ─────────

// TestForgery_UnregisteredKey_Rejected: a never-registered key gets
// UnknownKey(1), the tx still succeeds, actionState stays None.
func TestForgery_UnregisteredKey_Rejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	k := newSoftKey()
	qx, qy := softCoordinates(k)
	keyID := softKeyID(qx, qy)
	items := freshItems(3)
	action := signDirectAction(t, k, keyID, 1, items, freshDetailsHash(t))
	receipt := sendApplyActions(t, ctx, env, action)

	actionID := uuid.UUID(action.ActionId)
	reason, found := rejectedReason(t, receipt, actionID)
	require.True(t, found, "no ActionRejected log")
	require.Equal(t, uint8(1), reason, "want UnknownKey(1)")
	require.Equal(t, uint8(0), callActionState(t, ctx, env, actionID), "actionState must stay None")
}

// TestForgery_ForeignSignature_Rejected: a registered keyId signed by a
// different key gets BadSignature(3), actionState stays None.
func TestForgery_ForeignSignature_Rejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	foreign := newSoftKey()
	items := freshItems(3)
	action := signDirectAction(t, foreign, op.keyID, 1, items, freshDetailsHash(t))
	receipt := sendApplyActions(t, ctx, env, action)

	actionID := uuid.UUID(action.ActionId)
	reason, found := rejectedReason(t, receipt, actionID)
	require.True(t, found, "no ActionRejected log")
	require.Equal(t, uint8(3), reason, "want BadSignature(3)")
	require.Equal(t, uint8(0), callActionState(t, ctx, env, actionID), "actionState must stay None")
}

// TestForgery_Replay_NoStateChange: a valid applied action replayed byte for
// byte is skipped silently — no new logs, items and state untouched.
func TestForgery_Replay_NoStateChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	items := freshItems(3)
	action := signDirectAction(t, op.key, op.keyID, 1, items, freshDetailsHash(t))
	actionID := uuid.UUID(action.ActionId)

	receipt := sendApplyActions(t, ctx, env, action)
	require.True(t, appliedIn(receipt, actionID), "first send must apply the action")
	for i, it := range items {
		require.Equalf(t, statusAccepted, callItemStatus(t, ctx, env, it), "item %d must be Accepted(1)", i)
	}
	stateAfterFirst := callActionState(t, ctx, env, actionID)

	replay := sendApplyActions(t, ctx, env, action)
	require.False(t, appliedIn(replay, actionID), "replay must not re-apply")
	_, reRejected := rejectedReason(t, replay, actionID)
	require.False(t, reRejected, "replay must not reject")
	require.False(t, itemMovedIn(replay, actionID), "replay must not move items")
	for i, it := range items {
		require.Equalf(t, statusAccepted, callItemStatus(t, ctx, env, it), "item %d status must stay 1", i)
	}
	require.Equal(t, stateAfterFirst, callActionState(t, ctx, env, actionID), "actionState must not change")
}

// TestForgery_ModifiedAfterSignature_Rejected: calldata edited after signing
// breaks the challenge; the contract rejects with BadSignature(3) and the
// signed items keep status None.
func TestForgery_ModifiedAfterSignature_Rejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	t.Run("items", func(t *testing.T) {
		op := newSignedOperator(t, ctx, env)
		items := freshItems(2)
		action := signDirectAction(t, op.key, op.keyID, 1, items, freshDetailsHash(t))

		words := append(action.ItemIds, uuidToUint256(uuid.New().String()))
		sort.Slice(words, func(i, j int) bool { return words[i].Cmp(words[j]) < 0 })
		action.ItemIds = words

		receipt := sendApplyActions(t, ctx, env, action)
		reason, found := rejectedReason(t, receipt, uuid.UUID(action.ActionId))
		require.True(t, found, "no ActionRejected log")
		require.Equal(t, uint8(3), reason, "modified items must get BadSignature(3)")
		for i, it := range items {
			require.Equalf(t, uint8(0), callItemStatus(t, ctx, env, it), "signed item %d must keep status None", i)
		}
	})

	t.Run("details", func(t *testing.T) {
		op := newSignedOperator(t, ctx, env)
		items := freshItems(2)
		detailsHash := freshDetailsHash(t)
		action := signDirectAction(t, op.key, op.keyID, 1, items, detailsHash)

		action.DetailsHash[0] ^= 0xff

		receipt := sendApplyActions(t, ctx, env, action)
		reason, found := rejectedReason(t, receipt, uuid.UUID(action.ActionId))
		require.True(t, found, "no ActionRejected log")
		require.Equal(t, uint8(3), reason, "modified details must get BadSignature(3)")
		for i, it := range items {
			require.Equalf(t, uint8(0), callItemStatus(t, ctx, env, it), "signed item %d must keep status None", i)
		}
	})
}
