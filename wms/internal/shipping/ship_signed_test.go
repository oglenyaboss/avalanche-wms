package shipping

import (
	"bytes"
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

func bufferBinRecord(binID, destinationID uuid.UUID) *BufferBinRecord {
	section := binSectionShippingBuffer
	dest := destinationID
	return &BufferBinRecord{
		BinID:         binID,
		Code:          "SHIP-BUF-01",
		Section:       &section,
		DestinationID: &dest,
	}
}

func atGateDispatch(dispatchID, destinationID uuid.UUID) *DispatchRecord {
	return &DispatchRecord{
		DispatchID:    dispatchID,
		DispatchCode:  "DSP-001",
		DestinationID: destinationID,
		Status:        domain.OutboundDispatchStatusAtGate,
	}
}

// shipSigner returns an operator passkey whose key pair signs real assertions.
func shipSigner() (*domain.Passkey, *ecdsa.PrivateKey) {
	key := signedactiontest.NewKey()
	point := signedactiontest.Coordinates(key)
	return &domain.Passkey{
		CredentialID: []byte("cred-operator"),
		Key:          point,
		KeyID:        signedaction.KeyID(point),
		Status:       domain.PasskeyStatusActive,
	}, key
}

// newShipOperation builds a ship operation over the given items with the
// products' order mapping baked into the details hash.
func newShipOperation(t *testing.T, dispatchID, bufferBinID, operatorID uuid.UUID, orderByProduct map[uuid.UUID]uuid.UUID, items []uuid.UUID) (*signedops.Operation, *domain.Passkey, *ecdsa.PrivateKey) {
	t.Helper()
	signer, key := shipSigner()
	details, err := json.Marshal(shipDetails{
		DispatchID:          dispatchID,
		ShippingBufferBinID: bufferBinID,
		OccurredAtMs:        1760781780000,
	})
	if err != nil {
		t.Fatalf("marshal details: %v", err)
	}
	op, err := signedops.NewOperation(signedaction.TypeShip, operatorID, signer, details, items,
		func(salt [32]byte, partItems []uuid.UUID) [32]byte {
			orders := make([]uuid.UUID, 0, len(partItems))
			for _, id := range partItems {
				if orderID, ok := orderByProduct[id]; ok {
					orders = append(orders, orderID)
				}
			}
			return signedaction.ShipDetailsHash(salt, dispatchID, bufferBinID, orders, operatorID, 1760781780000)
		})
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	return op, signer, key
}

// signShipParts signs every part of the operation with the test key.
func signShipParts(t *testing.T, op *signedops.Operation, key *ecdsa.PrivateKey, credentialID []byte) map[uuid.UUID]signedaction.Assertion {
	t.Helper()
	assertions := map[uuid.UUID]signedaction.Assertion{}
	for _, p := range op.Parts {
		a := signedactiontest.Sign(key, p.Challenge)
		a.CredentialID = credentialID
		assertions[p.ActionID] = a
	}
	return assertions
}

func draftEnv(bufferBinID, dispatchID, destinationID uuid.UUID, products []ProductForShip) (*mockShippingRepo, *shipMockOps, *shipMockSigner) {
	signerPasskey, _ := shipSigner()
	repo := &mockShippingRepo{
		bin:          bufferBinRecord(bufferBinID, destinationID),
		dispatch:     atGateDispatch(dispatchID, destinationID),
		listProducts: products,
	}
	return repo, newShipMockOps(), &shipMockSigner{signer: signerPasskey}
}

// shipMockOps — хранилище операций в тестах подписанной отгрузки.
type shipMockOps struct {
	inserted   *signedops.Operation
	operations map[uuid.UUID]*signedops.Operation
}

func newShipMockOps() *shipMockOps {
	return &shipMockOps{operations: map[uuid.UUID]*signedops.Operation{}}
}

func (m *shipMockOps) InsertOperation(_ context.Context, op *signedops.Operation) error {
	m.inserted = op
	m.operations[op.OperationID] = op
	return nil
}

func (m *shipMockOps) Operation(_ context.Context, id uuid.UUID) (*signedops.Operation, error) {
	return m.operations[id], nil
}

// shipMockSigner — источник активного ключа в тестах подписанной отгрузки.
type shipMockSigner struct {
	signer    *domain.Passkey
	signerErr error
}

func (m *shipMockSigner) ActiveSigner(_ context.Context, _ uuid.UUID) (*domain.Passkey, error) {
	if m.signerErr != nil {
		return nil, m.signerErr
	}
	return m.signer, nil
}

func (m *shipMockSigner) RelyingParty() (string, []string) {
	return "localhost", []string{signedactiontest.Origin}
}

func TestDraftShip_Success_SplitsIntoParts(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	destinationID := uuid.New()

	// 2001 товар → 2 части: 2000 + 1 (лимит ADR-0001 §10).
	const total = signedops.MaxItemsPerAction + 1
	items := make([]uuid.UUID, total)
	orders := make([]uuid.UUID, 7)
	for i := range orders {
		orders[i] = uuid.New()
	}
	orderByProduct := make(map[uuid.UUID]uuid.UUID, total)
	for i := range items {
		items[i] = uuid.New()
		orderByProduct[items[i]] = orders[i%len(orders)]
	}
	products := make([]ProductForShip, total)
	for i, id := range items {
		order := orderByProduct[id]
		products[i] = ProductForShip{ProductID: id, OrderID: &order}
	}

	repo, ops, signer := draftEnv(bufferBinID, dispatchID, destinationID, products)
	svc := NewService(repo, ops, signer)

	op, rpID, err := svc.DraftShip(context.Background(), operatorID, bufferBinID, dispatchID, nil)
	if err != nil {
		t.Fatalf("DraftShip: %v", err)
	}
	if rpID != "localhost" {
		t.Fatalf("rpID = %q, want localhost", rpID)
	}
	if ops.inserted == nil || ops.inserted.OperationID != op.OperationID {
		t.Fatalf("operation not stored")
	}
	if op.ActionType != signedaction.TypeShip {
		t.Errorf("actionType = %d, want 4", op.ActionType)
	}
	if len(op.Parts) != 2 || len(op.Parts[0].ItemIDs) != signedops.MaxItemsPerAction || len(op.Parts[1].ItemIDs) != 1 {
		t.Fatalf("parts = %d (%d, %d), want 2 (%d, 1)",
			len(op.Parts), len(op.Parts[0].ItemIDs), len(op.Parts[1].ItemIDs), signedops.MaxItemsPerAction)
	}
	for _, p := range op.Parts {
		if !slices.IsSortedFunc(p.ItemIDs, func(a, b uuid.UUID) int {
			aItem := signedaction.ItemID(a)
			bItem := signedaction.ItemID(b)
			return bytes.Compare(aItem[:], bItem[:])
		}) {
			t.Errorf("part %s items are not sorted by itemId", p.ActionID)
		}
	}

	var details struct {
		DispatchID          uuid.UUID `json:"dispatch_id"`
		ShippingBufferBinID uuid.UUID `json:"shipping_buffer_bin_id"`
		OccurredAtMs        int64     `json:"occurred_at_ms"`
	}
	if err := json.Unmarshal(op.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v (%s)", err, op.Details)
	}
	if details.DispatchID != dispatchID || details.ShippingBufferBinID != bufferBinID {
		t.Errorf("details = %+v, want dispatch %s bin %s", details, dispatchID, bufferBinID)
	}
	if details.OccurredAtMs <= 0 || details.OccurredAtMs > time.Now().UnixMilli() {
		t.Errorf("occurred_at_ms = %d, want a fresh truncated ms timestamp", details.OccurredAtMs)
	}
	if time.UnixMilli(details.OccurredAtMs) != time.UnixMilli(details.OccurredAtMs).Truncate(time.Millisecond) {
		t.Errorf("occurred_at_ms = %d is not millisecond-precision", details.OccurredAtMs)
	}

	// detailsHash каждой части пересчитывается из заказов ИМЕННО этой части.
	for i, p := range op.Parts {
		partOrders := make([]uuid.UUID, 0, len(p.ItemIDs))
		for _, id := range p.ItemIDs {
			partOrders = append(partOrders, orderByProduct[id])
		}
		want := signedaction.ShipDetailsHash(p.Salt, dispatchID, bufferBinID, partOrders, operatorID, uint64(details.OccurredAtMs))
		if p.DetailsHash != want {
			t.Errorf("part %d detailsHash = %x, want %x", i, p.DetailsHash, want)
		}
	}

	// Врата неподтверждённой сборки получили весь набор товаров.
	if len(repo.unconfirmedAsked) != total {
		t.Errorf("unconfirmed gate got %d ids, want %d", len(repo.unconfirmedAsked), total)
	}
}

// insertProductIDs — товары одной записи shippings.
func insertProductIDs(ins shippingInsert) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ins.Events))
	for _, ev := range ins.Events {
		out = append(out, ev.ProductID)
	}
	return out
}

func TestDraftShip_Guards(t *testing.T) {
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	destinationID := uuid.New()
	operatorID := uuid.New()

	base := func() (*mockShippingRepo, *shipMockOps, *shipMockSigner) {
		return draftEnv(bufferBinID, dispatchID, destinationID, []ProductForShip{{ProductID: uuid.New()}})
	}
	call := func(svc *Service, bin, dispatch uuid.UUID, ids []uuid.UUID) error {
		_, _, err := svc.DraftShip(context.Background(), operatorID, bin, dispatch, ids)
		return err
	}

	t.Run("invalid input", func(t *testing.T) {
		repo, ops, signer := base()
		svc := NewService(repo, ops, signer)
		if err := call(svc, uuid.Nil, dispatchID, nil); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
		if err := call(svc, bufferBinID, uuid.Nil, nil); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("bin not shipping buffer", func(t *testing.T) {
		repo, ops, signer := base()
		repo.binErr = ErrBinNotFound
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, nil); !errors.Is(err, ErrBinNotFound) {
			t.Fatalf("err = %v, want ErrBinNotFound", err)
		}
		repo.binErr = nil
		section := "A"
		repo.bin = &BufferBinRecord{BinID: bufferBinID, Section: &section, DestinationID: &destinationID}
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, nil); !errors.Is(err, ErrBinNotShippingBuffer) {
			t.Fatalf("err = %v, want ErrBinNotShippingBuffer", err)
		}
		repo.bin = &BufferBinRecord{BinID: bufferBinID, Section: nil}
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, nil); !errors.Is(err, ErrBinNotShippingBuffer) {
			t.Fatalf("err = %v, want ErrBinNotShippingBuffer", err)
		}
	})
	t.Run("dispatch not found", func(t *testing.T) {
		repo, ops, signer := base()
		repo.dispatchErr = ErrDispatchNotFound
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, nil); !errors.Is(err, ErrDispatchNotFound) {
			t.Fatalf("err = %v, want ErrDispatchNotFound", err)
		}
	})
	t.Run("destination mismatch", func(t *testing.T) {
		repo, ops, signer := base()
		repo.dispatch = atGateDispatch(dispatchID, uuid.New())
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, nil); !errors.Is(err, ErrDestinationMismatch) {
			t.Fatalf("err = %v, want ErrDestinationMismatch", err)
		}
	})
	t.Run("dispatch not at gate", func(t *testing.T) {
		repo, ops, signer := base()
		repo.dispatch.Status = domain.OutboundDispatchStatusScheduled
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, nil); !errors.Is(err, ErrDispatchNotAtGate) {
			t.Fatalf("err = %v, want ErrDispatchNotAtGate", err)
		}
	})
	t.Run("spot product not in buffer", func(t *testing.T) {
		repo, ops, signer := base()
		repo.listProducts = []ProductForShip{{ProductID: uuid.New()}}
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, []uuid.UUID{uuid.New(), uuid.New()}); !errors.Is(err, ErrProductNotInBuffer) {
			t.Fatalf("err = %v, want ErrProductNotInBuffer", err)
		}
	})
	t.Run("bulk buffer empty", func(t *testing.T) {
		repo, ops, signer := base()
		repo.listProducts = nil
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, nil); !errors.Is(err, ErrBufferEmpty) {
			t.Fatalf("err = %v, want ErrBufferEmpty", err)
		}
	})
	t.Run("picking not confirmed", func(t *testing.T) {
		repo, ops, signer := base()
		repo.unconfirmedProducts = []uuid.UUID{uuid.New()}
		if err := call(NewService(repo, ops, signer), bufferBinID, dispatchID, nil); !errors.Is(err, ErrPickingNotConfirmed) {
			t.Fatalf("err = %v, want ErrPickingNotConfirmed", err)
		}
		if ops.inserted != nil {
			t.Fatal("draft must not be stored when picking is unconfirmed")
		}
	})
}

func TestSubmitShip_Errors(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	destinationID := uuid.New()
	order1 := uuid.New()
	items := []uuid.UUID{uuid.New(), uuid.New()}

	build := func(t *testing.T) (*mockShippingRepo, *shipMockOps, *shipMockSigner, *signedops.Operation, *ecdsa.PrivateKey) {
		t.Helper()
		orderByProduct := map[uuid.UUID]uuid.UUID{items[0]: order1, items[1]: order1}
		op, signerPasskey, key := newShipOperation(t, dispatchID, bufferBinID, operatorID, orderByProduct, items)
		locked := make([]ProductForShip, len(items))
		for i, id := range items {
			order := orderByProduct[id]
			locked[i] = ProductForShip{ProductID: id, OrderID: &order}
		}
		repo := &mockShippingRepo{
			bin:             bufferBinRecord(bufferBinID, destinationID),
			dispatch:        atGateDispatch(dispatchID, destinationID),
			lockedProducts:  locked,
			lockedOperation: op,
		}
		ops := newShipMockOps()
		ops.operations[op.OperationID] = op
		return repo, ops, &shipMockSigner{signer: signerPasskey}, op, key
	}

	t.Run("operation not found", func(t *testing.T) {
		repo, ops, signer, _, _ := build(t)
		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), operatorID, uuid.New(), nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("draft of another operator", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), uuid.New(), op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("wrong action type", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		op.ActionType = signedaction.TypePick
		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("already submitted", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		op.Submitted = true
		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationSubmitted) {
			t.Fatalf("err = %v, want ErrOperationSubmitted", err)
		}
	})
	t.Run("signer credential mismatch", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		other, _ := shipSigner()
		other.CredentialID = []byte("other-credential")
		signer.signer = other
		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrSignerNotActive) {
			t.Fatalf("err = %v, want ErrSignerNotActive", err)
		}
	})
	t.Run("bad assertion, nothing written", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		assertion := signedactiontest.Sign(signedactiontest.NewKey(), op.Parts[0].Challenge)
		assertion.CredentialID = op.SignerCredentialID
		assertions := map[uuid.UUID]signedaction.Assertion{op.Parts[0].ActionID: assertion}

		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, signedops.ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
		if repo.withTxCalls != 0 || repo.writeActionsCalls != 0 || len(repo.updatedProductIDs) != 0 || len(repo.insertedShippings) != 0 {
			t.Fatalf("nothing must be written on invalid assertion: tx=%d actions=%d shipped=%d shippings=%d",
				repo.withTxCalls, repo.writeActionsCalls, len(repo.updatedProductIDs), len(repo.insertedShippings))
		}
	})
	t.Run("draft stale: a signed product left the buffer", func(t *testing.T) {
		repo, ops, signer, op, key := build(t)
		repo.lockedProducts = repo.lockedProducts[:1]
		assertions := signShipParts(t, op, key, op.SignerCredentialID)
		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, ErrDraftStale) {
			t.Fatalf("err = %v, want ErrDraftStale", err)
		}
		if repo.writeActionsCalls != 0 || len(repo.updatedProductIDs) != 0 || len(repo.insertedShippings) != 0 {
			t.Fatalf("stale draft must write nothing: actions=%d shipped=%d shippings=%d",
				repo.writeActionsCalls, len(repo.updatedProductIDs), len(repo.insertedShippings))
		}
	})
	t.Run("signer lock rejected", func(t *testing.T) {
		repo, ops, signer, op, key := build(t)
		repo.lockSignerErr = signedops.ErrSignerNotActive
		assertions := signShipParts(t, op, key, op.SignerCredentialID)
		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, signedops.ErrSignerNotActive) {
			t.Fatalf("err = %v, want ErrSignerNotActive", err)
		}
		if repo.writeActionsCalls != 0 || len(repo.updatedProductIDs) != 0 {
			t.Fatalf("failed signer lock must write nothing: actions=%d shipped=%d", repo.writeActionsCalls, len(repo.updatedProductIDs))
		}
	})
	t.Run("picking unconfirmed at submit time", func(t *testing.T) {
		repo, ops, signer, op, key := build(t)
		repo.unconfirmedProducts = []uuid.UUID{items[1]}
		assertions := signShipParts(t, op, key, op.SignerCredentialID)
		_, err := NewService(repo, ops, signer).SubmitShip(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, ErrPickingNotConfirmed) {
			t.Fatalf("err = %v, want ErrPickingNotConfirmed", err)
		}
		if repo.withTxCalls != 0 || repo.writeActionsCalls != 0 {
			t.Fatalf("unconfirmed picking must block the tx: tx=%d actions=%d", repo.withTxCalls, repo.writeActionsCalls)
		}
	})
}

func TestSubmitShip_Success_TwoParts(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	destinationID := uuid.New()

	// 2001 товар → 2 части, у каждой свой action_id в shippings.
	const total = signedops.MaxItemsPerAction + 1
	items := make([]uuid.UUID, total)
	orders := make([]uuid.UUID, 3)
	for i := range orders {
		orders[i] = uuid.New()
	}
	orderByProduct := make(map[uuid.UUID]uuid.UUID, total)
	for i := range items {
		items[i] = uuid.New()
		orderByProduct[items[i]] = orders[i%len(orders)]
	}

	op, signerPasskey, key := newShipOperation(t, dispatchID, bufferBinID, operatorID, orderByProduct, items)
	var details struct {
		DispatchID          uuid.UUID `json:"dispatch_id"`
		ShippingBufferBinID uuid.UUID `json:"shipping_buffer_bin_id"`
		OccurredAtMs        int64     `json:"occurred_at_ms"`
	}
	if err := json.Unmarshal(op.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}

	locked := make([]ProductForShip, total)
	for i, id := range items {
		order := orderByProduct[id]
		locked[i] = ProductForShip{ProductID: id, OrderID: &order}
	}
	repo := &mockShippingRepo{
		bin:             bufferBinRecord(bufferBinID, destinationID),
		dispatch:        atGateDispatch(dispatchID, destinationID),
		lockedProducts:  locked,
		lockedOperation: op,
		ordersCompleted: 3,
		bufferRemaining: 0,
	}
	ops := newShipMockOps()
	ops.operations[op.OperationID] = op
	signer := &shipMockSigner{signer: signerPasskey}

	result, err := NewService(repo, ops, signer).
		SubmitShip(context.Background(), operatorID, op.OperationID, signShipParts(t, op, key, signerPasskey.CredentialID))
	if err != nil {
		t.Fatalf("SubmitShip: %v", err)
	}

	if result.ProductsShipped != total {
		t.Errorf("productsShipped = %d, want %d", result.ProductsShipped, total)
	}
	if result.OutboxEventsCreated != len(op.Parts) {
		t.Errorf("outboxEventsCreated = %d, want %d (parts)", result.OutboxEventsCreated, len(op.Parts))
	}
	if result.OrdersCompleted != 3 {
		t.Errorf("ordersCompleted = %d, want 3", result.OrdersCompleted)
	}
	if !result.DispatchDeparted || !repo.dispatchDeparted {
		t.Fatal("expected dispatch to depart when the buffer pool is empty")
	}
	if result.OperationID != op.OperationID.String() {
		t.Errorf("operationID = %s, want %s", result.OperationID, op.OperationID)
	}

	// Все подписанные товары переведены в SHIPPED.
	shipped := append([]uuid.UUID(nil), repo.updatedProductIDs...)
	slices.SortFunc(shipped, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	wantItems := append([]uuid.UUID(nil), items...)
	slices.SortFunc(wantItems, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	if !slices.Equal(shipped, wantItems) {
		t.Errorf("shipped = %d items, want all %d signed items", len(shipped), len(wantItems))
	}

	// Каждая часть пишет свою запись shippings со своим action_id и временем из деталей.
	if len(repo.insertedShippings) != len(op.Parts) {
		t.Fatalf("shippings inserts = %d, want %d (one per part)", len(repo.insertedShippings), len(op.Parts))
	}
	wantTime := time.UnixMilli(details.OccurredAtMs).UTC()
	for i, p := range op.Parts {
		ins := repo.insertedShippings[i]
		if ins.ActionID != p.ActionID {
			t.Errorf("insert %d action = %s, want part action %s", i, ins.ActionID, p.ActionID)
		}
		if !slices.Equal(insertProductIDs(ins), p.ItemIDs) {
			t.Errorf("insert %d products = %v, want %v", i, insertProductIDs(ins), p.ItemIDs)
		}
		if ins.DispatchID != dispatchID || ins.OperatorID != operatorID {
			t.Errorf("insert %d scoping = (%s, %s), want (%s, %s)", i, ins.DispatchID, ins.OperatorID, dispatchID, operatorID)
		}
		if !ins.OccurredAt.Equal(wantTime) {
			t.Errorf("insert %d occurredAt = %v, want details time %v", i, ins.OccurredAt, wantTime)
		}
		for _, ev := range ins.Events {
			if ev.EventID == uuid.Nil {
				t.Errorf("insert %d event lacks id", i)
			}
		}
	}

	// В продвижение заказов уходят различные заказы отгруженных товаров.
	wantOrders := make([]uuid.UUID, 0, len(orders))
	wantOrders = append(wantOrders, orders...)
	if len(repo.updatedOrderIDs) != len(wantOrders) {
		t.Fatalf("orders promoted = %v, want %v", repo.updatedOrderIDs, wantOrders)
	}
	for _, o := range wantOrders {
		if !slices.Contains(repo.updatedOrderIDs, o) {
			t.Errorf("orders promoted = %v, want contains %s", repo.updatedOrderIDs, o)
		}
	}

	if repo.writeActionsCalls != 1 {
		t.Fatalf("WriteActions calls = %d, want 1", repo.writeActionsCalls)
	}
	if repo.lastWriteActionsOp == nil || repo.lastWriteActionsOp.OperationID != op.OperationID {
		t.Fatalf("WriteActions got op %v, want %s", repo.lastWriteActionsOp, op.OperationID)
	}
	if len(repo.lastWriteActionsPayl) != len(op.Parts) {
		t.Fatalf("WriteActions payloads = %d, want %d", len(repo.lastWriteActionsPayl), len(op.Parts))
	}
}

func TestSubmitShip_PartialOrderStaysAtGate(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	destinationID := uuid.New()
	order1 := uuid.New()
	items := []uuid.UUID{uuid.New()}

	orderByProduct := map[uuid.UUID]uuid.UUID{items[0]: order1}
	op, signerPasskey, key := newShipOperation(t, dispatchID, bufferBinID, operatorID, orderByProduct, items)
	locked := []ProductForShip{{ProductID: items[0], OrderID: &order1}}
	repo := &mockShippingRepo{
		bin:                    bufferBinRecord(bufferBinID, destinationID),
		dispatch:               atGateDispatch(dispatchID, destinationID),
		lockedProducts:         locked,
		lockedOperation:        op,
		ordersPartiallyShipped: 1,
		bufferRemaining:        3,
	}
	ops := newShipMockOps()
	ops.operations[op.OperationID] = op
	signer := &shipMockSigner{signer: signerPasskey}

	result, err := NewService(repo, ops, signer).
		SubmitShip(context.Background(), operatorID, op.OperationID, signShipParts(t, op, key, signerPasskey.CredentialID))
	if err != nil {
		t.Fatalf("SubmitShip: %v", err)
	}
	if result.OrdersPartiallyShipped != 1 || result.OrdersCompleted != 0 {
		t.Fatalf("orders = (completed %d, partial %d), want (0, 1)", result.OrdersCompleted, result.OrdersPartiallyShipped)
	}
	if result.DispatchDeparted || repo.dispatchDeparted {
		t.Fatal("expected dispatch to stay at gate while the buffer has products")
	}
	if result.BufferRemaining != 3 {
		t.Fatalf("bufferRemaining = %d, want 3", result.BufferRemaining)
	}
}
