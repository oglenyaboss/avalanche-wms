//go:build e2e

package e2e

import (
	"context"
	"encoding/hex"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestSignedPutaway_EndToEnd drives the signed putaway end to end: a signed
// receive of 3 products → one signed PutAway action for the whole basket →
// outbox → adapter → SignedMappingWMS, and proves the draft challenge, the
// putaways rows, the on-chain events, the operation status and the timeline.
func TestSignedPutaway_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	const productCount = 3
	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, productCount)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, productCount)
	require.Equal(t, "TABLE_CLOSED", run.result.Status)

	for _, pid := range run.productIDs {
		waitForOnchainCommitted(t, ctx, env, eventIDForAggregate(t, ctx, env, pid, "receiving"), "receiving")
	}

	// ── Draft: action type 2, one part for the whole basket, ADR-0001 challenge ──
	draft := draftPlaceToStorageBin(t, env, op, run.productIDs, sc.StorageBinID)
	require.Equal(t, 2, draft.ActionType, "putaway draft is action type 2 (PutAway)")
	require.Len(t, draft.Parts, 1, "a basket of <2000 products drafts exactly one part")
	part := draft.Parts[0]
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
	require.ElementsMatch(t, run.productIDs, itemIDs, "the draft signs exactly the basket products")

	detailsHashBytes, err := hex.DecodeString(strings.TrimPrefix(part.DetailsHash, "0x"))
	require.NoError(t, err)
	var draftDetailsHash [32]byte
	copy(draftDetailsHash[:], detailsHashBytes)

	challengeRaw, err := decodeBase64URL(part.Challenge)
	require.NoError(t, err)
	var draftChallenge [32]byte
	copy(draftChallenge[:], challengeRaw)
	wantChallenge := softChallenge(actionID, 2, softItemsHash(itemIDs), draftDetailsHash)
	require.Equal(t, wantChallenge[:], draftChallenge[:], "draft challenge is not the ADR-0001 challenge")

	// ── Submit: the whole basket places with one signed part ──
	assertions := signDraft(t, op, draft)
	result := submitPlaceToStorageBin(t, env, op.token, draft.OperationID, assertions)
	require.Equal(t, productCount, result.ProductsPlaced)
	require.Equal(t, 1, result.OutboxEventsCreated, "one signed part for the whole basket")
	require.Equal(t, draft.OperationID, result.OperationID)

	for _, pid := range run.productIDs {
		requireProductStatus(t, ctx, env, pid, "STORED")
	}

	// ── DB: every putaways row references the part's action id, the storage bin
	// and the signing operator; the details hash is reproducible from those rows. ──
	var firstBinID, firstOperator uuid.UUID
	var firstOccurredAt time.Time
	for i, pid := range run.productIDs {
		var paActionID, paBinID, paOperator uuid.UUID
		var paOccurredAt time.Time
		err := env.db.QueryRow(ctx, `
			SELECT action_id, bin_id, operator_id, occurred_at
			FROM wms_ops.putaways
			WHERE product_id = $1`, pid).Scan(&paActionID, &paBinID, &paOperator, &paOccurredAt)
		require.NoError(t, err, "putaways row for product %d", i)
		require.Equal(t, actionID, paActionID, "putaways row must reference the signed part")
		require.Equal(t, sc.StorageBinID, paBinID)
		require.Equal(t, op.userID, paOperator)
		if i == 0 {
			firstBinID, firstOperator, firstOccurredAt = paBinID, paOperator, paOccurredAt
		}
	}

	var saltBytes []byte
	err = env.db.QueryRow(ctx, `
		SELECT salt FROM public.warehouse_actions WHERE action_id = $1`, actionID).Scan(&saltBytes)
	require.NoError(t, err)
	var salt [32]byte
	copy(salt[:], saltBytes)
	wantDetailsHash := softPutAwayDetailsHash(salt, firstBinID, firstOperator, uint64(firstOccurredAt.UnixMilli()))
	require.Equal(t, wantDetailsHash, draftDetailsHash,
		"details_hash must be reproducible from the putaways domain row, not the details JSON")

	// ── Chain: the action commits, the operation is APPLIED, items are PutAway(2) ──
	waitForOnchainCommitted(t, ctx, env, actionID, "putaway")
	status := waitForOperationState(t, ctx, env, op.token, draft.OperationID, "APPLIED", nil)
	require.Len(t, status.Parts, 1)
	require.Equal(t, part.ActionID, status.Parts[0].ActionID)
	require.Nil(t, status.Parts[0].RejectReason)

	for i, pid := range run.productIDs {
		require.Equalf(t, statusPutAway, callItemStatus(t, ctx, env, pid),
			"product %d on-chain status should be PutAway(2) after the signed putaway", i)
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
			require.Equal(t, byte(2), data[31], "actionType PutAway(2)")
			require.Equal(t, wantDetailsHash[:], data[32:64], "ActionApplied data detailsHash")
			require.Equal(t, int64(productCount), bigIntFromWord(data[64:96]).Int64(), "ActionApplied itemCount")
		case len(lg.Topics) == 3 && lg.Topics[0] == itemMovedTopic:
			require.Equal(t, actionIDTopic, lg.Topics[2], "ItemMoved topic2 = actionId")
			require.Equal(t, byte(2), lg.Data[31], "ItemMoved status PutAway(2)")
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

	// ── Timeline: the signed action is the putaway step of the product ──
	var timeline traceTimeline
	getJSON(t, env, op.token, "/products/timeline?key="+run.productIDs[0].String(), &timeline)
	putawayStep := false
	for _, s := range timeline.Steps {
		if s.Stage == "putaway" && s.EventID == part.ActionID {
			putawayStep = true
		}
	}
	require.True(t, putawayStep, "timeline must show the signed action as the putaway step (event_id = action_id)")

	// ── The next stage passes the gate: allocate the order and pick product 0 ──
	suf := shortSuffix()
	orderNo := "ORD-SPA-" + suf
	dispatchCode := "DSP-SPA-" + suf

	var destinationID uuid.UUID
	err = env.db.QueryRow(ctx, `
		SELECT d.destination_id
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.warehouse_id = w.warehouse_id AND d.code = $1
		WHERE w.name = $2`, destinationCode, warehouseName).Scan(&destinationID)
	require.NoError(t, err, "resolve SHOP-7 destination")

	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.orders(order_id,external_order_no,customer_id,warehouse_id,destination_id,status,created_at,updated_at)
		SELECT gen_random_uuid(),$1,u.user_id,w.warehouse_id,d.destination_id,'NEW'::wms_inventory.order_status,now(),now()
		FROM public.users u
		JOIN wms_inventory.warehouses w ON w.name=$2
		JOIN wms_inventory.destinations d ON d.code=$3 AND d.warehouse_id=w.warehouse_id
		WHERE u.username='customer'`, orderNo, warehouseName, destinationCode)
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.order_lines(order_id,sku_id,qty)
		SELECT o.order_id,s.sku_id,$3
		FROM wms_inventory.orders o
		JOIN wms_inventory.skus s ON s.name=$2
		WHERE o.external_order_no=$1`, orderNo, outboundSeedSKUName, productCount)
	mustExec(t, ctx, env, `
		INSERT INTO wms_inventory.outbound_dispatches(dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1,w.warehouse_id,d.destination_id,'E2ESPA','E2E SPA Driver','+70000000003','SCHEDULED'::wms_inventory.outbound_dispatch_status,now()+interval '1 day',now(),now()
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.code=$2 AND d.warehouse_id=w.warehouse_id
		WHERE w.name=$3`, dispatchCode, destinationCode, warehouseName)

	var allocated allocateData
	postJSON(t, env, op.token, "/assembly/allocate", map[string]string{
		"destination_id": destinationID.String(),
	}, &allocated)
	require.GreaterOrEqual(t, allocated.AllocatedOrders, 1)
	require.GreaterOrEqual(t, allocated.AllocatedProducts, productCount)

	var tasks taskListData
	getJSON(t, env, op.token, "/assembly/tasks?destination_id="+url.QueryEscape(destinationID.String()), &tasks)
	require.ElementsMatch(t, uuidStrings(run.productIDs), productIDsFromTasks(tasks.Tasks),
		"all placed products must be pending pick tasks")

	// The pick runs the chain gate on the previous stage: it must succeed.
	var picked pickData
	postJSON(t, env, op.token, "/assembly/pick", map[string]string{
		"product_id": run.productIDs[0].String(),
	}, &picked)
	require.Equal(t, run.productIDs[0].String(), picked.ProductID)
	requireProductStatus(t, ctx, env, run.productIDs[0], "ASSEMBLED")
}

// TestSignedPutaway_DraftStaleWhenProductMoved proves the draft-stale guard: a
// draft signed over a basket whose product was already placed by another
// operation is rejected with 409 DRAFT_STALE and writes nothing.
func TestSignedPutaway_DraftStaleWhenProductMoved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, 2)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, 2)
	require.Len(t, run.productIDs, 2)

	for _, pid := range run.productIDs {
		waitForOnchainCommitted(t, ctx, env, eventIDForAggregate(t, ctx, env, pid, "receiving"), "receiving")
	}

	// Draft 1 covers both products; product 0 is then placed by a second
	// draft+submit, so draft 1's signed basket no longer matches the DB.
	staleDraft := draftPlaceToStorageBin(t, env, op, run.productIDs, sc.StorageBinID)
	require.Len(t, staleDraft.Parts, 1)
	staleActionID, err := uuid.Parse(staleDraft.Parts[0].ActionID)
	require.NoError(t, err)

	fresh := placeToStorageBinSigned(t, env, op, []uuid.UUID{run.productIDs[0]}, sc.StorageBinID)
	require.Equal(t, 1, fresh.ProductsPlaced)
	requireProductStatus(t, ctx, env, run.productIDs[0], "STORED")

	// Submitting the stale draft: signatures are valid, the DB state is not.
	staleAssertions := signDraft(t, op, staleDraft)
	status, code, _ := submitPlaceToStorageBinRaw(t, env, op.token, staleDraft.OperationID, staleAssertions)
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "DRAFT_STALE", code)

	// Product 1 stays RECEIVED in the buffer and draft 1's action never reached
	// the outbox.
	requireProductStatus(t, ctx, env, run.productIDs[1], "RECEIVED")
	require.Equal(t, 0, outboxCountForEvent(t, ctx, env, staleActionID),
		"a stale draft must not emit its signed action")
}

// TestSignedPutaway_RejectedActionBlocksPick simulates a chain rejection of the
// committed signed putaway (reject_reason 12, WrongItemStatus) and proves the
// pick gate blocks the product: the WMS must not advance a product whose prior
// stage diverged from the ledger.
func TestSignedPutaway_RejectedActionBlocksPick(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, 1)
	run := receiveAndCloseSigned(t, ctx, env, op, sc, 1)

	waitForOnchainCommitted(t, ctx, env, eventIDForAggregate(t, ctx, env, run.productIDs[0], "receiving"), "receiving")

	draft := draftPlaceToStorageBin(t, env, op, []uuid.UUID{run.productIDs[0]}, sc.StorageBinID)
	require.Len(t, draft.Parts, 1)
	actionID, err := uuid.Parse(draft.Parts[0].ActionID)
	require.NoError(t, err)
	putaway := submitPlaceToStorageBin(t, env, op.token, draft.OperationID, signDraft(t, op, draft))
	require.Equal(t, 1, putaway.ProductsPlaced)
	require.Equal(t, draft.OperationID, putaway.OperationID)

	waitForOnchainCommitted(t, ctx, env, actionID, "putaway")

	// Simulate the contract rejecting the action: FAILED + RejectReason 12.
	mustExec(t, ctx, env, `
		UPDATE public.onchain_events
		SET status = 'FAILED', reject_reason = 12, updated_at = now()
		WHERE event_id = $1`, actionID)

	// The pick runs the chain gate on the putaway stage BEFORE any task lookup,
	// so the blocked product is refused with 409 CHAIN_EVENT_REJECTED.
	status, code, _ := postExpectError(t, env, op.token, "/assembly/pick", map[string]string{
		"product_id": run.productIDs[0].String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "CHAIN_EVENT_REJECTED", code)

	// The product stays STORED and no picking event may exist.
	requireProductStatus(t, ctx, env, run.productIDs[0], "STORED")
	require.Equal(t, 0, outboxCountForAggregate(t, ctx, env, run.productIDs[0], "picking"),
		"gated pick must not emit a picking event")
}
