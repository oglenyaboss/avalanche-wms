package assembly

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedaction"
	"wms/internal/signedaction/signedactiontest"
	"wms/internal/signedops"
)

func shippingBufferBin(binID, destinationID uuid.UUID) *domain.Bin {
	dest := destinationID
	return &domain.Bin{
		BinID:         binID,
		Code:          "SHIP-BUF-01",
		Section:       stringPtr("SHIPPING_BUFFER"),
		DestinationID: &dest,
	}
}

func TestDraftUnloadToShippingBuffer_Success(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	destinationID := uuid.New()
	order1 := uuid.New()
	order2 := uuid.New()
	p1, p2, p3, p4 := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	basket := []BasketItem{
		{ProductID: p1, OrderID: &order1},
		{ProductID: p2, OrderID: &order1},
		{ProductID: p3, OrderID: &order2},
		{ProductID: p4, OrderID: nil}, // заказ удалён в середине потока
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}
	signerPasskey, _ := signedSigner()
	repo := &mockAssemblyRepo{
		shippingBufferBin: shippingBufferBin(bufferBinID, destinationID),
		basket:            basket,
	}

	op, rpID, err := NewService(repo, ops, &mockSignerSource{signer: signerPasskey, rpID: "localhost", origins: []string{signedactiontest.Origin}}).
		DraftUnloadToShippingBuffer(context.Background(), operatorID, bufferBinID)
	if err != nil {
		t.Fatalf("DraftUnloadToShippingBuffer: %v", err)
	}
	if rpID != "localhost" {
		t.Fatalf("rpID = %q, want localhost", rpID)
	}
	if ops.inserted == nil || ops.inserted.OperationID != op.OperationID {
		t.Fatalf("operation not stored")
	}
	if op.ActionType != signedaction.TypePick {
		t.Errorf("actionType = %d, want 3", op.ActionType)
	}
	if len(op.Parts) != 1 || len(op.Parts[0].ItemIDs) != 4 {
		t.Fatalf("parts = %+v, want one part with all four products", op.Parts)
	}
	wantItems := signedaction.SortByItemID([]uuid.UUID{p1, p2, p3, p4})
	for i, id := range wantItems {
		if op.Parts[0].ItemIDs[i] != id {
			t.Errorf("items[%d] = %s, want %s (sorted by itemId)", i, op.Parts[0].ItemIDs[i], id)
		}
	}

	var details struct {
		ShippingBufferBinID uuid.UUID `json:"shipping_buffer_bin_id"`
		OccurredAtMs        int64     `json:"occurred_at_ms"`
	}
	if err := json.Unmarshal(op.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v (%s)", err, op.Details)
	}
	if details.ShippingBufferBinID != bufferBinID {
		t.Errorf("details shipping_buffer_bin_id = %s, want %s", details.ShippingBufferBinID, bufferBinID)
	}
	if details.OccurredAtMs <= 0 || details.OccurredAtMs > time.Now().UnixMilli() {
		t.Errorf("occurred_at_ms = %d, want a fresh truncated ms timestamp", details.OccurredAtMs)
	}
	if time.UnixMilli(details.OccurredAtMs) != time.UnixMilli(details.OccurredAtMs).Truncate(time.Millisecond) {
		t.Errorf("occurred_at_ms = %d is not millisecond-precision", details.OccurredAtMs)
	}

	// detailsHash пересчитывается из заказов ЧАСТИ: p2 дублирует заказ p1,
	// товар без заказа не даёт записи — уникальные [order1, order2].
	want := signedaction.PickDetailsHash(op.Parts[0].Salt, bufferBinID, []uuid.UUID{order1, order2}, operatorID, uint64(details.OccurredAtMs))
	if op.Parts[0].DetailsHash != want {
		t.Errorf("detailsHash = %x, want %x", op.Parts[0].DetailsHash, want)
	}
}

func TestDraftUnloadToShippingBuffer_Errors(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	destinationID := uuid.New()

	base := func() (*mockAssemblyRepo, *mockOperationStore, *mockSignerSource) {
		signer, _ := signedSigner()
		return &mockAssemblyRepo{
			shippingBufferBin: shippingBufferBin(bufferBinID, destinationID),
		}, &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}, &mockSignerSource{signer: signer}
	}

	t.Run("invalid input", func(t *testing.T) {
		repo, ops, signer := base()
		svc := NewService(repo, ops, signer)
		if _, _, err := svc.DraftUnloadToShippingBuffer(context.Background(), uuid.Nil, bufferBinID); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
		if _, _, err := svc.DraftUnloadToShippingBuffer(context.Background(), operatorID, uuid.Nil); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("bin not shipping buffer", func(t *testing.T) {
		repo, ops, signer := base()
		repo.shippingBufferBinErr = ErrBinNotShippingBuffer
		if _, _, err := NewService(repo, ops, signer).DraftUnloadToShippingBuffer(context.Background(), operatorID, bufferBinID); !errors.Is(err, ErrBinNotShippingBuffer) {
			t.Fatalf("err = %v, want ErrBinNotShippingBuffer", err)
		}
	})
	t.Run("bin without destination", func(t *testing.T) {
		repo, ops, signer := base()
		repo.shippingBufferBin = &domain.Bin{BinID: bufferBinID, DestinationID: nil}
		if _, _, err := NewService(repo, ops, signer).DraftUnloadToShippingBuffer(context.Background(), operatorID, bufferBinID); !errors.Is(err, ErrDestinationNotFound) {
			t.Fatalf("err = %v, want ErrDestinationNotFound", err)
		}
	})
	t.Run("cart empty", func(t *testing.T) {
		repo, ops, signer := base()
		repo.basket = nil // корзина для этого назначения пуста и в целом пуста
		if _, _, err := NewService(repo, ops, signer).DraftUnloadToShippingBuffer(context.Background(), operatorID, bufferBinID); !errors.Is(err, ErrCartEmpty) {
			t.Fatalf("err = %v, want ErrCartEmpty", err)
		}
	})
	t.Run("destination mismatch", func(t *testing.T) {
		repo, ops, signer := base()
		repo.basket = nil
		repo.assembledProducts = []uuid.UUID{uuid.New(), uuid.New()} // несёт товары другого магазина
		if _, _, err := NewService(repo, ops, signer).DraftUnloadToShippingBuffer(context.Background(), operatorID, bufferBinID); !errors.Is(err, ErrDestinationMismatch) {
			t.Fatalf("err = %v, want ErrDestinationMismatch", err)
		}
	})
}

func TestSubmitUnloadToShippingBuffer_Errors(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	destinationID := uuid.New()
	order1 := uuid.New()
	items := []uuid.UUID{uuid.New(), uuid.New()}

	build := func(t *testing.T) (*mockAssemblyRepo, *mockOperationStore, *mockSignerSource, *signedops.Operation, *ecdsa.PrivateKey) {
		t.Helper()
		orderByProduct := map[uuid.UUID]uuid.UUID{items[0]: order1, items[1]: order1}
		op, signerPasskey, key := newPickOperation(t, bufferBinID, operatorID, orderByProduct, items)
		repo := &mockAssemblyRepo{
			shippingBufferBin: shippingBufferBin(bufferBinID, destinationID),
			lockedBasket: []BasketItem{
				{ProductID: items[0], OrderID: &order1},
				{ProductID: items[1], OrderID: &order1},
			},
			lockedOperation: op,
		}
		ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{op.OperationID: op}}
		return repo, ops, &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}, op, key
	}

	t.Run("operation not found", func(t *testing.T) {
		repo, ops, signer, _, _ := build(t)
		_, err := NewService(repo, ops, signer).SubmitUnloadToShippingBuffer(context.Background(), operatorID, uuid.New(), nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("draft of another operator", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		_, err := NewService(repo, ops, signer).SubmitUnloadToShippingBuffer(context.Background(), uuid.New(), op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("wrong action type", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		op.ActionType = signedaction.TypeReceive
		_, err := NewService(repo, ops, signer).SubmitUnloadToShippingBuffer(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("already submitted", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		op.Submitted = true
		_, err := NewService(repo, ops, signer).SubmitUnloadToShippingBuffer(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationSubmitted) {
			t.Fatalf("err = %v, want ErrOperationSubmitted", err)
		}
	})
	t.Run("signer credential mismatch", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		other, _ := signedSigner()
		other.CredentialID = []byte("other-credential")
		signer.signer = other
		_, err := NewService(repo, ops, signer).SubmitUnloadToShippingBuffer(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrSignerNotActive) {
			t.Fatalf("err = %v, want ErrSignerNotActive", err)
		}
	})
	t.Run("bad assertion, nothing written", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		assertion := signedactiontest.Sign(signedactiontest.NewKey(), op.Parts[0].Challenge)
		assertion.CredentialID = op.SignerCredentialID
		assertions := map[uuid.UUID]signedaction.Assertion{op.Parts[0].ActionID: assertion}

		_, err := NewService(repo, ops, signer).SubmitUnloadToShippingBuffer(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, signedops.ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
		if repo.withTxCalls != 0 || repo.writeActionsCalls != 0 || len(repo.movedToBuffer) != 0 || len(repo.stamps) != 0 {
			t.Fatalf("nothing must be written on invalid assertion: tx=%d actions=%d moved=%d stamps=%d",
				repo.withTxCalls, repo.writeActionsCalls, len(repo.movedToBuffer), len(repo.stamps))
		}
	})
	t.Run("draft stale: basket no longer matches the signed set", func(t *testing.T) {
		repo, ops, signer, op, key := build(t)
		// Один товар уже не в корзине (ASSEMBLED с DONE-задачей) под это назначение.
		repo.lockedBasket = repo.lockedBasket[:1]
		assertions := signParts(t, op, key, op.SignerCredentialID)
		_, err := NewService(repo, ops, signer).SubmitUnloadToShippingBuffer(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, ErrDraftStale) {
			t.Fatalf("err = %v, want ErrDraftStale", err)
		}
		if repo.writeActionsCalls != 0 || len(repo.movedToBuffer) != 0 || len(repo.stamps) != 0 {
			t.Fatalf("stale draft must write nothing: actions=%d moved=%d stamps=%d", repo.writeActionsCalls, len(repo.movedToBuffer), len(repo.stamps))
		}
	})
	t.Run("signer lock rejected", func(t *testing.T) {
		repo, ops, signer, op, key := build(t)
		repo.lockSignerErr = signedops.ErrSignerNotActive
		assertions := signParts(t, op, key, op.SignerCredentialID)
		_, err := NewService(repo, ops, signer).SubmitUnloadToShippingBuffer(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, signedops.ErrSignerNotActive) {
			t.Fatalf("err = %v, want ErrSignerNotActive", err)
		}
		if repo.writeActionsCalls != 0 || len(repo.movedToBuffer) != 0 {
			t.Fatalf("failed signer lock must write nothing: actions=%d moved=%d", repo.writeActionsCalls, len(repo.movedToBuffer))
		}
	})
}

func TestSubmitUnloadToShippingBuffer_Success(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	destinationID := uuid.New()
	order1 := uuid.New()
	order2 := uuid.New()
	items := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	orderByProduct := map[uuid.UUID]uuid.UUID{
		items[0]: order1,
		items[1]: order1,
		items[2]: order2,
		items[3]: order1,
	}

	op, signerPasskey, key := newPickOperation(t, bufferBinID, operatorID, orderByProduct, items)
	var details struct {
		ShippingBufferBinID uuid.UUID `json:"shipping_buffer_bin_id"`
		OccurredAtMs        int64     `json:"occurred_at_ms"`
	}
	if err := json.Unmarshal(op.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}

	basket := make([]BasketItem, len(items))
	for i, id := range items {
		order := orderByProduct[id]
		basket[i] = BasketItem{ProductID: id, OrderID: &order}
	}
	repo := &mockAssemblyRepo{
		shippingBufferBin: shippingBufferBin(bufferBinID, destinationID),
		lockedBasket:      basket,
		lockedOperation:   op,
		ordersUpdated:     2,
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{op.OperationID: op}}
	signerSource := &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}

	result, err := NewService(repo, ops, signerSource).
		SubmitUnloadToShippingBuffer(context.Background(), operatorID, op.OperationID, signParts(t, op, key, signerPasskey.CredentialID))
	if err != nil {
		t.Fatalf("SubmitUnloadToShippingBuffer: %v", err)
	}

	if result.BufferBinID != bufferBinID.String() {
		t.Errorf("bufferBinID = %s, want %s", result.BufferBinID, bufferBinID)
	}
	if result.ProductsPlaced != len(items) {
		t.Errorf("productsPlaced = %d, want %d", result.ProductsPlaced, len(items))
	}
	if result.OrdersAssembled != 2 {
		t.Errorf("ordersAssembled = %d, want 2", result.OrdersAssembled)
	}
	if result.OperationID != op.OperationID.String() {
		t.Errorf("operationID = %s, want %s", result.OperationID, op.OperationID)
	}

	// Товары перемещены в подписанную ячейку буфера отгрузки.
	if !slices.Equal(signedaction.SortByItemID(repo.movedToBuffer), signedaction.SortByItemID(items)) || repo.movedBufferBinID != bufferBinID {
		t.Errorf("moved = %v to bin %s, want %v to %s", repo.movedToBuffer, repo.movedBufferBinID, items, bufferBinID)
	}

	// Каждая часть проставила своей части товаров свой action_id и время из деталей.
	if len(repo.stamps) != len(op.Parts) {
		t.Fatalf("stamps = %d, want %d", len(repo.stamps), len(op.Parts))
	}
	wantTime := time.UnixMilli(details.OccurredAtMs).UTC()
	for i, p := range op.Parts {
		stamp := repo.stamps[i]
		if stamp.ActionID != p.ActionID {
			t.Errorf("stamp %d action = %s, want part action %s", i, stamp.ActionID, p.ActionID)
		}
		if !slices.Equal(stamp.ProductIDs, p.ItemIDs) {
			t.Errorf("stamp %d products = %v, want %v", i, stamp.ProductIDs, p.ItemIDs)
		}
		if stamp.OperatorID != operatorID || stamp.DestinationID != destinationID {
			t.Errorf("stamp %d scoping = (%s, %s), want (%s, %s)", i, stamp.OperatorID, stamp.DestinationID, operatorID, destinationID)
		}
		if !stamp.OccurredAt.Equal(wantTime) {
			t.Errorf("stamp %d occurredAt = %v, want details time %v", i, stamp.OccurredAt, wantTime)
		}
	}

	// В продвижение заказов уходят различные не-NULL заказы заблокированной корзины.
	if len(repo.lastOrderIDs) != 2 || !slices.Contains(repo.lastOrderIDs, order1) || !slices.Contains(repo.lastOrderIDs, order2) {
		t.Errorf("orders promoted = %v, want distinct [%s %s]", repo.lastOrderIDs, order1, order2)
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
