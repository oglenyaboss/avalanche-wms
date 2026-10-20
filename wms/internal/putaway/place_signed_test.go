package putaway

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/ledger"
	"wms/internal/signedaction"
	"wms/internal/signedaction/signedactiontest"
	"wms/internal/signedops"
)

func TestDraftPlaceToStorageBin_Success(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	bufferBinID := uuid.New()
	p1 := uuid.New()
	p2 := uuid.New()

	repo := &mockPutawayRepo{
		storageBin: &domain.Bin{BinID: storageBinID, Code: "M2-A-03"},
		productsMap: map[uuid.UUID]*domain.Product{
			p1: {ProductID: p1, Status: "RECEIVED", BinID: &bufferBinID},
			p2: {ProductID: p2, Status: "RECEIVED", BinID: &bufferBinID},
		},
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}
	signerPasskey, _ := signedSigner()
	signerSource := &mockSignerSource{signer: signerPasskey, rpID: "localhost", origins: []string{signedactiontest.Origin}}

	op, rpID, err := putawayService(repo, ops, signerSource).
		DraftPlaceToStorageBin(context.Background(), operatorID, []uuid.UUID{p1, p2, p1}, storageBinID)
	if err != nil {
		t.Fatalf("DraftPlaceToStorageBin: %v", err)
	}
	if rpID != "localhost" {
		t.Fatalf("rpID = %q, want localhost", rpID)
	}
	if ops.inserted == nil || ops.inserted.OperationID != op.OperationID {
		t.Fatalf("operation not stored")
	}
	if op.ActionType != signedaction.TypePutAway {
		t.Errorf("actionType = %d, want 2", op.ActionType)
	}
	if len(op.Parts) != 1 || len(op.Parts[0].ItemIDs) != 2 {
		t.Fatalf("parts = %+v, want one part with the deduplicated pair", op.Parts)
	}
	wantItems := signedaction.SortByItemID([]uuid.UUID{p1, p2})
	for i, id := range wantItems {
		if op.Parts[0].ItemIDs[i] != id {
			t.Errorf("items[%d] = %s, want %s (sorted by itemId)", i, op.Parts[0].ItemIDs[i], id)
		}
	}

	var details struct {
		StorageBinID uuid.UUID `json:"storage_bin_id"`
		OccurredAtMs int64     `json:"occurred_at_ms"`
	}
	if err := json.Unmarshal(op.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v (%s)", err, op.Details)
	}
	if details.StorageBinID != storageBinID {
		t.Errorf("details storage_bin_id = %s, want %s", details.StorageBinID, storageBinID)
	}
	if details.OccurredAtMs <= 0 || details.OccurredAtMs > time.Now().UnixMilli() {
		t.Errorf("occurred_at_ms = %d, want a fresh truncated ms timestamp", details.OccurredAtMs)
	}
	if time.UnixMilli(details.OccurredAtMs) != time.UnixMilli(details.OccurredAtMs).Truncate(time.Millisecond) {
		t.Errorf("occurred_at_ms = %d is not millisecond-precision", details.OccurredAtMs)
	}
	want := signedaction.PutAwayDetailsHash(op.Parts[0].Salt, storageBinID, operatorID, uint64(details.OccurredAtMs))
	if op.Parts[0].DetailsHash != want {
		t.Errorf("detailsHash = %x, want %x", op.Parts[0].DetailsHash, want)
	}
}

func TestDraftPlaceToStorageBin_Errors(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	bufferBinID := uuid.New()
	productID := uuid.New()

	base := func() (*mockPutawayRepo, *mockOperationStore, *mockSignerSource) {
		repo := &mockPutawayRepo{
			storageBin: &domain.Bin{BinID: storageBinID, Code: "M2-A-03"},
			productsMap: map[uuid.UUID]*domain.Product{
				productID: {ProductID: productID, Status: "RECEIVED", BinID: &bufferBinID},
			},
		}
		signer, _ := signedSigner()
		return repo, &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}, &mockSignerSource{signer: signer}
	}

	t.Run("invalid input", func(t *testing.T) {
		repo, ops, signer := base()
		svc := putawayService(repo, ops, signer)
		if _, _, err := svc.DraftPlaceToStorageBin(context.Background(), uuid.Nil, []uuid.UUID{productID}, storageBinID); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
		if _, _, err := svc.DraftPlaceToStorageBin(context.Background(), operatorID, []uuid.UUID{productID}, uuid.Nil); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
		if _, _, err := svc.DraftPlaceToStorageBin(context.Background(), operatorID, nil, storageBinID); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("storage bin not found", func(t *testing.T) {
		repo, ops, signer := base()
		repo.storageBinErr = ErrStorageBinNotFound
		if _, _, err := putawayService(repo, ops, signer).DraftPlaceToStorageBin(context.Background(), operatorID, []uuid.UUID{productID}, storageBinID); !errors.Is(err, ErrStorageBinNotFound) {
			t.Fatalf("err = %v, want ErrStorageBinNotFound", err)
		}
	})
	t.Run("product not found", func(t *testing.T) {
		repo, ops, signer := base()
		repo.productsMap = nil
		if _, _, err := putawayService(repo, ops, signer).DraftPlaceToStorageBin(context.Background(), operatorID, []uuid.UUID{productID}, storageBinID); !errors.Is(err, ErrProductNotFound) {
			t.Fatalf("err = %v, want ErrProductNotFound", err)
		}
	})
	t.Run("product not in buffer", func(t *testing.T) {
		repo, ops, signer := base()
		repo.bufferBinErr = ErrBufferBinNotFound // bin товара не буферный → drift
		if _, _, err := putawayService(repo, ops, signer).DraftPlaceToStorageBin(context.Background(), operatorID, []uuid.UUID{productID}, storageBinID); !errors.Is(err, ErrProductNotInBuffer) {
			t.Fatalf("err = %v, want ErrProductNotInBuffer", err)
		}
	})
	t.Run("product not received", func(t *testing.T) {
		repo, ops, signer := base()
		repo.productsMap[productID].Status = "STORED"
		if _, _, err := putawayService(repo, ops, signer).DraftPlaceToStorageBin(context.Background(), operatorID, []uuid.UUID{productID}, storageBinID); !errors.Is(err, ErrProductNotReceived) {
			t.Fatalf("err = %v, want ErrProductNotReceived", err)
		}
	})
	t.Run("gate rejected before any write", func(t *testing.T) {
		repo, ops, signer := base()
		repo.checkChainErr = ledger.ErrChainEventRejected
		_, _, err := putawayService(repo, ops, signer).DraftPlaceToStorageBin(context.Background(), operatorID, []uuid.UUID{productID}, storageBinID)
		if !errors.Is(err, ledger.ErrChainEventRejected) {
			t.Fatalf("err = %v, want ErrChainEventRejected", err)
		}
		if repo.checkChainAgg != ledger.AggregateReceiving {
			t.Fatalf("gate aggregate = %q, want %q", repo.checkChainAgg, ledger.AggregateReceiving)
		}
		if repo.withTxCalls != 0 || ops.inserted != nil {
			t.Fatalf("gate must reject before tx and operation store; withTxCalls=%d inserted=%v", repo.withTxCalls, ops.inserted)
		}
	})
}

func TestSubmitPlaceToStorageBin_Errors(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	bufferBinID := uuid.New()
	items := []uuid.UUID{uuid.New(), uuid.New()}

	build := func(t *testing.T) (*mockPutawayRepo, *mockOperationStore, *mockSignerSource, *signedops.Operation, *ecdsa.PrivateKey) {
		t.Helper()
		op, signerPasskey, key := newSignedOperation(t, storageBinID, operatorID, items)
		repo := &mockPutawayRepo{
			storageBin:      &domain.Bin{BinID: storageBinID, Code: "M2-A-03"},
			productsMap:     map[uuid.UUID]*domain.Product{},
			lockedOperation: op,
		}
		for _, id := range items {
			bin := bufferBinID
			repo.productsMap[id] = &domain.Product{ProductID: id, Status: "RECEIVED", BinID: &bin}
		}
		ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{op.OperationID: op}}
		return repo, ops, &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}, op, key
	}

	t.Run("operation not found", func(t *testing.T) {
		repo, ops, signer, _, _ := build(t)
		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), operatorID, uuid.New(), nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("draft of another operator", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), uuid.New(), op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("wrong action type", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		op.ActionType = signedaction.TypeReceive
		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("already submitted", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		op.Submitted = true
		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationSubmitted) {
			t.Fatalf("err = %v, want ErrOperationSubmitted", err)
		}
	})
	t.Run("signer credential mismatch", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		other, _ := signedSigner()
		other.CredentialID = []byte("other-credential")
		signer.signer = other
		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrSignerNotActive) {
			t.Fatalf("err = %v, want ErrSignerNotActive", err)
		}
	})
	t.Run("bad assertion, nothing written", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		assertion := signedactiontest.Sign(signedactiontest.NewKey(), op.Parts[0].Challenge)
		assertion.CredentialID = op.SignerCredentialID
		assertions := map[uuid.UUID]signedaction.Assertion{op.Parts[0].ActionID: assertion}

		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, signedops.ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
		if repo.withTxCalls != 0 || repo.writeActionsCalls != 0 || len(repo.insertedPutaways) != 0 || len(repo.updatedProductIDs) != 0 {
			t.Fatalf("nothing must be written on invalid assertion: tx=%d actions=%d putaways=%d updates=%d",
				repo.withTxCalls, repo.writeActionsCalls, len(repo.insertedPutaways), len(repo.updatedProductIDs))
		}
	})
	t.Run("draft stale: product no longer RECEIVED", func(t *testing.T) {
		repo, ops, signer, op, key := build(t)
		for _, id := range items {
			repo.productsMap[id].Status = "STORED"
		}
		assertions := signParts(t, op, key, op.SignerCredentialID)
		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, ErrDraftStale) {
			t.Fatalf("err = %v, want ErrDraftStale", err)
		}
		if repo.writeActionsCalls != 0 || len(repo.insertedPutaways) != 0 {
			t.Fatalf("stale draft must write nothing: actions=%d putaways=%d", repo.writeActionsCalls, len(repo.insertedPutaways))
		}
	})
	t.Run("draft stale: product left the buffer", func(t *testing.T) {
		repo, ops, signer, op, key := build(t)
		repo.bufferBinErr = ErrBufferBinNotFound // текущий bin товара больше не буфер
		assertions := signParts(t, op, key, op.SignerCredentialID)
		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, ErrDraftStale) {
			t.Fatalf("err = %v, want ErrDraftStale", err)
		}
	})
	t.Run("signer lock rejected", func(t *testing.T) {
		repo, ops, signer, op, key := build(t)
		repo.lockSignerErr = signedops.ErrSignerNotActive
		assertions := signParts(t, op, key, op.SignerCredentialID)
		_, err := putawayService(repo, ops, signer).SubmitPlaceToStorageBin(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, signedops.ErrSignerNotActive) {
			t.Fatalf("err = %v, want ErrSignerNotActive", err)
		}
		if repo.writeActionsCalls != 0 || len(repo.insertedPutaways) != 0 {
			t.Fatalf("failed signer lock must write nothing: actions=%d putaways=%d", repo.writeActionsCalls, len(repo.insertedPutaways))
		}
	})
}

// signParts signs every part of the operation with the test key.
func signParts(t *testing.T, op *signedops.Operation, key *ecdsa.PrivateKey, credentialID []byte) map[uuid.UUID]signedaction.Assertion {
	t.Helper()
	assertions := map[uuid.UUID]signedaction.Assertion{}
	for _, p := range op.Parts {
		a := signedactiontest.Sign(key, p.Challenge)
		a.CredentialID = credentialID
		assertions[p.ActionID] = a
	}
	return assertions
}

func TestSubmitPlaceToStorageBin_Success(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	bufferBinID := uuid.New()
	items := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	op, signerPasskey, key := newSignedOperation(t, storageBinID, operatorID, items)
	var details struct {
		StorageBinID uuid.UUID `json:"storage_bin_id"`
		OccurredAtMs int64     `json:"occurred_at_ms"`
	}
	if err := json.Unmarshal(op.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}

	repo := &mockPutawayRepo{
		storageBin:      &domain.Bin{BinID: storageBinID, Code: "M2-A-03"},
		productsMap:     map[uuid.UUID]*domain.Product{},
		lockedOperation: op,
	}
	for _, id := range items {
		bin := bufferBinID
		repo.productsMap[id] = &domain.Product{ProductID: id, Status: "RECEIVED", BinID: &bin}
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{op.OperationID: op}}
	signerSource := &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}

	result, err := putawayService(repo, ops, signerSource).
		SubmitPlaceToStorageBin(context.Background(), operatorID, op.OperationID, signParts(t, op, key, signerPasskey.CredentialID))
	if err != nil {
		t.Fatalf("SubmitPlaceToStorageBin: %v", err)
	}

	if result.StorageBinID != storageBinID.String() || result.StorageBinCode != "M2-A-03" {
		t.Errorf("storage bin = %s/%s, want %s/M2-A-03", result.StorageBinID, result.StorageBinCode, storageBinID)
	}
	if result.ProductsPlaced != len(items) {
		t.Errorf("productsPlaced = %d, want %d", result.ProductsPlaced, len(items))
	}
	if result.OutboxEventsCreated != len(op.Parts) {
		t.Errorf("outboxEventsCreated = %d, want %d", result.OutboxEventsCreated, len(op.Parts))
	}
	if result.OperationID != op.OperationID.String() {
		t.Errorf("operationID = %s, want %s", result.OperationID, op.OperationID)
	}

	// Каждый товар перемещён в подписанную ячейку...
	updated := map[uuid.UUID]uuid.UUID{}
	for i, id := range repo.updatedProductIDs {
		updated[id] = repo.updatedBinIDs[i]
	}
	for _, id := range items {
		if got := updated[id]; got != storageBinID {
			t.Errorf("product %s updated to bin %s, want %s", id, got, storageBinID)
		}
	}
	// ...и получил putaway-строку, ссылающуюся на свою часть операции.
	if len(repo.insertedPutaways) != len(items) {
		t.Fatalf("putaway rows = %d, want %d", len(repo.insertedPutaways), len(items))
	}
	partByProduct := map[uuid.UUID]uuid.UUID{}
	for _, p := range op.Parts {
		for _, id := range p.ItemIDs {
			partByProduct[id] = p.ActionID
		}
	}
	for _, row := range repo.insertedPutaways {
		if row.ActionID != partByProduct[row.ProductID] {
			t.Errorf("putaway for %s carries action %s, want part action %s", row.ProductID, row.ActionID, partByProduct[row.ProductID])
		}
		if row.FromBinID != bufferBinID || row.BinID != storageBinID {
			t.Errorf("putaway for %s bins = %s→%s, want %s→%s", row.ProductID, row.FromBinID, row.BinID, bufferBinID, storageBinID)
		}
		if row.OperatorID != operatorID {
			t.Errorf("putaway for %s operator = %s, want %s", row.ProductID, row.OperatorID, operatorID)
		}
		if !row.OccurredAt.Equal(time.UnixMilli(details.OccurredAtMs).UTC()) {
			t.Errorf("putaway for %s occurredAt = %v, want details time %v", row.ProductID, row.OccurredAt, time.UnixMilli(details.OccurredAtMs).UTC())
		}
		if row.EventID == uuid.Nil {
			t.Errorf("putaway for %s has no event id", row.ProductID)
		}
	}

	if repo.writeActionsCalls != 1 {
		t.Fatalf("WriteActions calls = %d, want 1", repo.writeActionsCalls)
	}
	if repo.lastWriteActionsOp == nil || repo.lastWriteActionsOp.OperationID != op.OperationID {
		t.Fatalf("WriteActions got op %v, want %s", repo.lastWriteActionsOp, op.OperationID)
	}
	if len(repo.lastWriteActionsPayloads) != len(op.Parts) {
		t.Fatalf("WriteActions payloads = %d, want %d", len(repo.lastWriteActionsPayloads), len(op.Parts))
	}
}
