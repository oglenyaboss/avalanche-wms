//go:build e2e

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// receivingScaffoldDTO mirrors receivingScaffold with a resolved cargoplace.
// (receivingScaffold itself is defined in fullchain_test.go.)

// signedCloseRun carries everything a signed-close assertion needs: the draft,
// the submit result and the product ids that went into the operation.
type signedCloseRun struct {
	draft      closeDraftDTO
	result     closeCargoplaceResultDTO
	productIDs []uuid.UUID
}

// receiveAndCloseSigned drives the receiving-table flow for productCount
// products of one scaffold cargoplace and closes it as a signed operation.
func receiveAndCloseSigned(t *testing.T, ctx context.Context, env *env, op *signedOperator, sc receivingScaffold, productCount int) signedCloseRun {
	t.Helper()

	postJSON[map[string]any](t, env, op.token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	}, nil)

	var box scanBoxData
	postJSON(t, env, op.token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_barcode":   "BOX-SR-" + shortSuffix(),
	}, &box)
	require.Equal(t, "OPEN", box.Status)

	var sku scanSKUData
	postJSON(t, env, op.token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       outboundSeedBarcode,
	}, &sku)
	require.Equal(t, sc.SKUID, sku.SKUID)

	run := signedCloseRun{}
	for i := 0; i < productCount; i++ {
		if i > 0 {
			var skuAgain scanSKUData
			postJSON(t, env, op.token, "/receiving/table/scan-sku", map[string]string{
				"cargoplace_id": sc.CargoplaceID.String(),
				"box_id":        box.BoxID.String(),
				"barcode":       outboundSeedBarcode,
			}, &skuAgain)
		}
		var scanned scanQRData
		postJSON(t, env, op.token, "/receiving/table/scan-qr", map[string]string{
			"cargoplace_id": sc.CargoplaceID.String(),
			"box_id":        box.BoxID.String(),
			"sku_id":        sku.SKUID.String(),
			"qr_code":       "QR-SR-" + shortSuffix() + "-" + uuid.NewString()[:8],
		}, &scanned)
		require.Equal(t, "RECEIVED", scanned.Status, "product %d should be RECEIVED", i)
		run.productIDs = append(run.productIDs, scanned.ProductID)
	}

	postJSON[map[string]any](t, env, op.token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	postJSON[map[string]any](t, env, op.token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)

	run.draft = draftCloseCargoplace(t, env, op, sc.CargoplaceID.String())
	assertions := signDraft(t, op, run.draft)
	run.result = submitCloseCargoplace(t, env, op.token, run.draft.OperationID, assertions)
	return run
}

// TestSignedReceive_EndToEnd drives the signed cargoplace close end to end:
// draft (challenge/items hash checked against independent keccak) → operator
// passkey signatures → submit → outbox → adapter → SignedMappingWMS, and proves
// the on-chain ActionApplied/ItemMoved events, the operation status view, the
// putaway gate and the product timeline.
func TestSignedReceive_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 3
	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, productCount)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, productCount)

	require.Equal(t, "TABLE_CLOSED", run.result.Status)
	require.Equal(t, 1, run.result.OutboxEventsCreated, "3 products close with exactly one signed part")

	// ── Draft contract (ADR-0001 §3/§9 shape) ──
	require.Equal(t, 1, run.draft.ActionType)
	require.Equal(t, "localhost", run.draft.RPID)
	require.Equal(t, "required", run.draft.UserVerification)
	require.Equal(t, 60000, run.draft.Timeout)
	require.NotEmpty(t, run.draft.AllowCredentials)
	require.Equal(t, "public-key", run.draft.AllowCredentials[0].Type)
	require.Equal(t, "0x"+hex.EncodeToString(op.keyID[:]), run.draft.SignerKeyID)

	require.Len(t, run.draft.Parts, 1)
	part := run.draft.Parts[0]
	actionID, err := uuid.Parse(part.ActionID)
	require.NoError(t, err)

	itemIDs := make([]uuid.UUID, 0, len(part.ItemIDs))
	for i, raw := range part.ItemIDs {
		id, err := uuid.Parse(raw)
		require.NoError(t, err)
		itemIDs = append(itemIDs, id)
		if i > 0 {
			require.Negativef(t,
				uuidToUint256(part.ItemIDs[i-1]).Cmp(uuidToUint256(raw)),
				"item_ids must ascend by itemId (uint256), position %d", i)
		}
	}
	require.ElementsMatch(t, run.productIDs, itemIDs, "the draft signs exactly the received products")

	detailsHashBytes, err := hex.DecodeString(strings.TrimPrefix(part.DetailsHash, "0x"))
	require.NoError(t, err)
	var draftDetailsHash [32]byte
	copy(draftDetailsHash[:], detailsHashBytes)

	challengeRaw, err := decodeBase64URL(part.Challenge)
	require.NoError(t, err)
	var draftChallenge [32]byte
	copy(draftChallenge[:], challengeRaw)
	wantChallenge := softChallenge(actionID, 1, softItemsHash(itemIDs), draftDetailsHash)
	require.Equal(t, wantChallenge[:], draftChallenge[:], "draft challenge is not the ADR-0001 challenge")

	// ── DB: one warehouse_actions row per part + one signed_action outbox row ──
	var dbActionType int16
	var dbItemIDs []uuid.UUID
	var submittedAt *time.Time
	var dbOperationID uuid.UUID
	err = env.db.QueryRow(ctx, `
		SELECT action_type, item_ids, submitted_at, operation_id
		FROM public.warehouse_actions
		WHERE action_id = $1`, actionID).Scan(&dbActionType, &dbItemIDs, &submittedAt, &dbOperationID)
	require.NoError(t, err)
	require.EqualValues(t, 1, dbActionType)
	require.ElementsMatch(t, itemIDs, dbItemIDs)
	require.NotNil(t, submittedAt, "submit must set submitted_at")
	require.Equal(t, run.draft.OperationID, dbOperationID.String())

	var aggID uuid.UUID
	var aggType string
	err = env.db.QueryRow(ctx, `
		SELECT aggregate_id, aggregate_type
		FROM public.outbox_events
		WHERE event_id = $1`, actionID).Scan(&aggID, &aggType)
	require.NoError(t, err)
	require.Equal(t, actionID, aggID, "event_id = aggregate_id = action_id")
	require.Equal(t, "signed_action", aggType)

	// Details from the CLOSE_CARGO log row and the action's salt: they must reproduce the on-chain detailsHash.
	var saltBytes []byte
	var detailsCargoplace string
	var detailsOperator uuid.UUID
	var occurredAt time.Time
	err = env.db.QueryRow(ctx, `
		SELECT wa.salt, rt.cargoplace_id::text, rt.operator_id, rt.occurred_at
		FROM public.warehouse_actions wa
		JOIN wms_ops.receiving_table rt ON rt.operation_id = wa.operation_id
		WHERE wa.operation_id = $1 AND rt.action = 'CLOSE_CARGO'
		LIMIT 1`, run.draft.OperationID).Scan(&saltBytes, &detailsCargoplace, &detailsOperator, &occurredAt)
	require.NoError(t, err)
	var salt [32]byte
	copy(salt[:], saltBytes)
	require.Equal(t, sc.CargoplaceID.String(), detailsCargoplace)
	require.Equal(t, op.userID, detailsOperator)

	wantDetailsHash := softReceiveDetailsHash(salt, sc.CargoplaceID, op.userID, uint64(occurredAt.UnixMilli()))
	require.Equal(t, wantDetailsHash, draftDetailsHash, "draft details_hash must be reproducible from the DB details")

	// ── Chain: the action commits and every product becomes Accepted(1) ──
	waitForOnchainCommitted(t, ctx, env, actionID, "receiving")
	status := waitForOperationState(t, ctx, env, op.token, run.draft.OperationID, "APPLIED", nil)
	require.Equal(t, run.draft.OperationID, status.OperationID)
	require.Len(t, status.Parts, 1)
	require.Equal(t, part.ActionID, status.Parts[0].ActionID)
	require.Nil(t, status.Parts[0].RejectReason)

	for i, pid := range run.productIDs {
		require.Equalf(t, statusAccepted, callItemStatus(t, ctx, env, pid),
			"product %d on-chain status should be Accepted(1) after the signed receive", i)
	}

	// ── Receipt logs: ActionApplied + ItemMoved of the WMS contract ──
	var txHash string
	err = env.db.QueryRow(ctx, `
		SELECT tx_hash FROM public.onchain_events WHERE event_id = $1`, actionID).Scan(&txHash)
	require.NoError(t, err)
	require.NotEmpty(t, txHash)

	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()
	receipt, err := client.TransactionReceipt(ctx, common.HexToHash(txHash))
	require.NoError(t, err)

	actionAppliedTopic := crypto.Keccak256Hash([]byte("ActionApplied(bytes16,bytes32,bytes16,uint8,bytes32,uint256)"))
	itemMovedTopic := crypto.Keccak256Hash([]byte("ItemMoved(uint256,bytes16,uint8)"))
	var actionIDTopic common.Hash
	copy(actionIDTopic[:16], actionID[:])

	appliedFound := false
	movedByItem := map[uuid.UUID]bool{}
	for _, lg := range receipt.Logs {
		if lg.Address != common.HexToAddress(env.contractAddr) {
			continue
		}
		switch {
		case len(lg.Topics) == 4 && lg.Topics[0] == actionAppliedTopic:
			appliedFound = true
			require.Equal(t, actionIDTopic, lg.Topics[1], "ActionApplied topic1 = actionId")
			require.Equal(t, common.BytesToHash(op.keyID[:]), lg.Topics[2], "ActionApplied topic2 = signer keyId")
			data := lg.Data
			require.GreaterOrEqual(t, len(data), 96)
			require.Equal(t, byte(1), data[31], "actionType Receive(1)")
			require.Equal(t, wantDetailsHash[:], data[32:64], "ActionApplied data detailsHash")
			require.Equal(t, int64(productCount), bigIntFromWord(data[64:96]).Int64(), "ActionApplied itemCount")
		case len(lg.Topics) == 3 && lg.Topics[0] == itemMovedTopic:
			require.Equal(t, actionIDTopic, lg.Topics[2], "ItemMoved topic2 = actionId")
			require.Equal(t, byte(1), lg.Data[31], "ItemMoved status Accepted(1)")
			itemID := new(big.Int).SetBytes(lg.Topics[1][:])
			found := false
			for _, pid := range run.productIDs {
				if uuidToUint256(pid.String()).Cmp(itemID) == 0 {
					found = true
					movedByItem[pid] = true
				}
			}
			require.True(t, found, "ItemMoved for an unknown item %s", itemID.Text(16))
		}
	}
	require.True(t, appliedFound, "receipt has no ActionApplied log")
	require.Len(t, movedByItem, productCount, "receipt must carry one ItemMoved per product")

	// ── The gate accepts the next stage and the timeline shows the action ──
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-product", map[string]string{
		"product_id":    run.productIDs[0].String(),
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)
	putaway := placeToStorageBinSigned(t, env, op, []uuid.UUID{run.productIDs[0]}, sc.StorageBinID)
	require.Equal(t, 1, putaway.ProductsPlaced, "putaway must pass the chain-status gate after a committed receive")
	require.Equal(t, 1, putaway.OutboxEventsCreated, "one product places with exactly one signed part")

	var timeline traceTimeline
	getJSON(t, env, op.token, "/products/timeline?key="+run.productIDs[0].String(), &timeline)
	receivingStep := false
	for _, s := range timeline.Steps {
		if s.Stage == "receiving" && s.EventID == part.ActionID {
			receivingStep = true
		}
	}
	require.True(t, receivingStep, "timeline must show the signed action as the receiving step (event_id = action_id)")
}

// decodeBase64URL decodes a raw (no padding) base64url string.
func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// TestSignedReceive_BadSignatureWritesNothing proves a broken signature writes
// nothing at all: the cargoplace stays TABLE_IN_PROGRESS, the operation stays a
// draft (submitted_at NULL) and no outbox row appears for the part.
func TestSignedReceive_BadSignatureWritesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, 1)
	receiveAndCloseSignedPrep(t, ctx, env, op, sc)

	draft := draftCloseCargoplace(t, env, op, sc.CargoplaceID.String())
	require.Len(t, draft.Parts, 1)
	part := draft.Parts[0]

	// Sign a DIFFERENT challenge: the signature does not verify against the part.
	alienChallenge := softChallenge(uuid.New(), 1, softItemsHash(nil), [32]byte{})
	bad := softSign(op.key, alienChallenge)
	status, code, _ := postExpectError(t, env, op.token, "/receiving/table/close-cargoplace/submit", map[string]any{
		"operation_id": draft.OperationID,
		"assertions": []map[string]string{{
			"action_id":          part.ActionID,
			"credential_id":      base64.RawURLEncoding.EncodeToString(op.credID),
			"authenticator_data": base64.RawURLEncoding.EncodeToString(bad.AuthenticatorData),
			"client_data_json":   base64.RawURLEncoding.EncodeToString(bad.ClientDataJSON),
			"signature":          base64.RawURLEncoding.EncodeToString(bad.Signature),
		}},
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "INVALID_ASSERTION", code)

	// Missing assertion list entirely: the empty list matches no part, so the
	// submit fails with 400 INVALID_ASSERTION and writes nothing.
	status, code, _ = postExpectError(t, env, op.token, "/receiving/table/close-cargoplace/submit", map[string]any{
		"operation_id": draft.OperationID,
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "INVALID_ASSERTION", code)

	// Nothing was written: cargoplace still in progress, action still a draft,
	// no outbox row for the part.
	var cpStatus string
	err := env.db.QueryRow(ctx, `
		SELECT status::text FROM wms_inventory.cargoplaces WHERE cargoplace_id = $1`,
		sc.CargoplaceID).Scan(&cpStatus)
	require.NoError(t, err)
	require.Equal(t, "TABLE_IN_PROGRESS", cpStatus)

	actionID, err := uuid.Parse(part.ActionID)
	require.NoError(t, err)
	require.Equal(t, 0, outboxCountForEvent(t, ctx, env, actionID))

	var submitted *time.Time
	err = env.db.QueryRow(ctx, `
		SELECT submitted_at FROM public.warehouse_actions WHERE action_id = $1`, actionID).Scan(&submitted)
	require.NoError(t, err)
	require.Nil(t, submitted, "a rejected submit must not mark the action submitted")
}

// receiveAndCloseSignedPrep drives the receiving flow up to (but not including)
// the signed close: scan-cargoplace → box → sku → qr → close-box → scan-buffer.
func receiveAndCloseSignedPrep(t *testing.T, ctx context.Context, env *env, op *signedOperator, sc receivingScaffold) {
	t.Helper()

	postJSON[map[string]any](t, env, op.token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	}, nil)

	var box scanBoxData
	postJSON(t, env, op.token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_barcode":   "BOX-SR-" + shortSuffix(),
	}, &box)

	var sku scanSKUData
	postJSON(t, env, op.token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       outboundSeedBarcode,
	}, &sku)

	var scanned scanQRData
	postJSON(t, env, op.token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       "QR-SR-" + shortSuffix() + "-" + uuid.NewString()[:8],
	}, &scanned)
	require.Equal(t, "RECEIVED", scanned.Status)

	postJSON[map[string]any](t, env, op.token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	postJSON[map[string]any](t, env, op.token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)
}

// TestSignedReceive_RejectedActionBlocksPutaway simulates a chain rejection of
// the committed signed receive (like the other gate tests inject FAILED) and
// proves the putaway gate blocks the product while the operation status view
// shows REJECTED with the contract's reason.
func TestSignedReceive_RejectedActionBlocksPutaway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, 1)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, 1)
	require.Len(t, run.draft.Parts, 1)

	actionID, err := uuid.Parse(run.draft.Parts[0].ActionID)
	require.NoError(t, err)
	waitForOnchainCommitted(t, ctx, env, actionID, "receiving")

	// Simulate the contract rejecting the action: FAILED + RejectReason 12.
	mustExec(t, ctx, env, `
		UPDATE public.onchain_events
		SET status = 'FAILED', reject_reason = 12, updated_at = now()
		WHERE event_id = $1`, actionID)

	// Putaway of the product is blocked by the chain-status gate — the DRAFT already
	// runs it, so no operation is even drafted.
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-product", map[string]string{
		"product_id":    run.productIDs[0].String(),
		"buffer_bin_id": sc.ReceivingBinID.String(),
	}, nil)
	status, code, _ := draftPlaceToStorageBinRaw(t, env, op.token, []string{run.productIDs[0].String()}, sc.StorageBinID.String())
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "CHAIN_EVENT_REJECTED", code)

	// The operation status view derives the rejection from the chain.
	waitForOperationState(t, ctx, env, op.token, run.draft.OperationID, "REJECTED", int16Ref(12))
}
