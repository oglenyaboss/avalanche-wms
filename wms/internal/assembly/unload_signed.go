package assembly

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedaction"
	"wms/internal/signedops"
)

// operationStore persists multi-part signed warehouse operations.
type operationStore interface {
	InsertOperation(ctx context.Context, op *signedops.Operation) error
	Operation(ctx context.Context, operationID uuid.UUID) (*signedops.Operation, error)
}

// signerSource returns the caller's active passkey and the WebAuthn relying party.
type signerSource interface {
	ActiveSigner(ctx context.Context, userID uuid.UUID) (*domain.Passkey, error)
	RelyingParty() (rpID string, origins []string)
}

// pickDetails are the draft inputs hashed into every part's detailsHash. The
// orderIds are not stored: they are read back from the products the parts move
// (ADR §4 — every details field must come from the domain tables the action changes).
type pickDetails struct {
	ShippingBufferBinID uuid.UUID `json:"shipping_buffer_bin_id"`
	OccurredAtMs        int64     `json:"occurred_at_ms"`
}

// DraftUnloadToShippingBuffer builds the signed Pick operation for unloading the
// operator's basket into a shipping buffer bin: the basket (product ASSEMBLED with
// a DONE task of this operator for the buffer's destination) is read-only, split
// into parts of at most MaxItemsPerAction, each part waits for its own passkey
// signature. Nothing is written except the draft itself.
func (s *Service) DraftUnloadToShippingBuffer(ctx context.Context, operatorID, bufferBinID uuid.UUID) (*signedops.Operation, string, error) {
	if operatorID == uuid.Nil || bufferBinID == uuid.Nil {
		return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer: %w", ErrInvalidInput)
	}

	bufferBin, err := s.repo.GetShippingBufferBinByID(ctx, bufferBinID)
	if err != nil {
		return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer get bin: %w", err)
	}
	if bufferBin.DestinationID == nil {
		return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer: %w", ErrDestinationNotFound)
	}

	basket, err := s.repo.ListOperatorBasket(ctx, operatorID, *bufferBin.DestinationID)
	if err != nil {
		return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer list basket: %w", err)
	}
	// Nothing to unload for THIS buffer's destination. Distinguish the two causes so
	// the operator gets an actionable message: if they are still carrying ASSEMBLED
	// products (picked for some other destination), they scanned the WRONG store's
	// buffer → DESTINATION_MISMATCH. Otherwise the cart is genuinely empty / already
	// placed (e.g. a duplicate scan) → CART_EMPTY. One COUNT on a rare, no-op path.
	if len(basket) == 0 {
		assembled, err := s.repo.CountAssembledByOperator(ctx, operatorID)
		if err != nil {
			return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer count assembled: %w", err)
		}
		if assembled > 0 {
			return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer: %w", ErrDestinationMismatch)
		}
		return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer: %w", ErrCartEmpty)
	}

	signer, err := s.signer.ActiveSigner(ctx, operatorID)
	if err != nil {
		return nil, "", err
	}

	// Время обрезается до миллисекунд, иначе detailsHash не воспроизвести из БД (ADR §4).
	occurredAt := time.Now().UTC().Truncate(time.Millisecond)
	details, err := json.Marshal(pickDetails{ShippingBufferBinID: bufferBinID, OccurredAtMs: occurredAt.UnixMilli()})
	if err != nil {
		return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer marshal details: %w", err)
	}

	items := make([]uuid.UUID, len(basket))
	orderByProduct := make(map[uuid.UUID]uuid.UUID, len(basket))
	for i, it := range basket {
		items[i] = it.ProductID
		if it.OrderID != nil {
			orderByProduct[it.ProductID] = *it.OrderID
		}
	}

	op, err := signedops.NewOperation(signedaction.TypePick, operatorID, signer, details, items,
		func(salt [32]byte, partItems []uuid.UUID) [32]byte {
			orders := make([]uuid.UUID, 0, len(partItems))
			for _, id := range partItems {
				if orderID, ok := orderByProduct[id]; ok {
					orders = append(orders, orderID)
				}
			}
			return signedaction.PickDetailsHash(salt, bufferBinID, orders, operatorID, uint64(occurredAt.UnixMilli()))
		})
	if err != nil {
		return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer new operation: %w", err)
	}

	if err := s.ops.InsertOperation(ctx, op); err != nil {
		return nil, "", fmt.Errorf("assembly.Service.DraftUnloadToShippingBuffer insert: %w", err)
	}
	rpID, _ := s.signer.RelyingParty()
	return op, rpID, nil
}

// SubmitUnloadToShippingBuffer verifies the operator's signatures over every part
// and applies the whole operation in one repository transaction. Signature errors
// are returned as is: nothing is written before every part verifies.
func (s *Service) SubmitUnloadToShippingBuffer(ctx context.Context, operatorID, operationID uuid.UUID, assertions map[uuid.UUID]signedaction.Assertion) (*ScanShippingBufferResponse, error) {
	op, err := s.ops.Operation(ctx, operationID)
	if err != nil {
		return nil, fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer load: %w", err)
	}
	if op == nil || op.CreatedBy != operatorID || op.ActionType != signedaction.TypePick {
		return nil, fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer: %w", signedops.ErrOperationNotFound)
	}
	if op.Submitted {
		return nil, fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer: %w", signedops.ErrOperationSubmitted)
	}

	signer, err := s.signer.ActiveSigner(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(signer.CredentialID, op.SignerCredentialID) {
		return nil, fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer: %w", signedops.ErrSignerNotActive)
	}

	rpID, origins := s.signer.RelyingParty()
	payloads, err := signedops.Envelopes(op, signer, assertions, rpID, origins)
	if err != nil {
		return nil, err
	}

	var details pickDetails
	if err := json.Unmarshal(op.Details, &details); err != nil {
		return nil, fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer decode details: %w", err)
	}
	occurredAt := time.UnixMilli(details.OccurredAtMs).UTC()

	items := make([]uuid.UUID, 0, len(op.Parts))
	for _, p := range op.Parts {
		items = append(items, p.ItemIDs...)
	}

	var bufferBin *domain.Bin
	var ordersAssembled int
	err = s.repo.WithTx(ctx, func(txRepo assemblyRepository) error {
		var err error
		bufferBin, err = txRepo.GetShippingBufferBinByID(ctx, details.ShippingBufferBinID)
		if err != nil {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer get bin: %w", err)
		}
		if bufferBin.DestinationID == nil {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer: %w", ErrDestinationNotFound)
		}

		// ADR §7: сначала блокируем строки товаров FOR UPDATE в детерминированном
		// порядке (по возрастанию product_id), затем операцию и ключ подписанта.
		basket, err := txRepo.LockOperatorBasket(ctx, operatorID, *bufferBin.DestinationID)
		if err != nil {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer lock basket: %w", err)
		}

		lockedOp, err := txRepo.LockOperation(ctx, operationID, operatorID)
		if err != nil {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer lock operation: %w", err)
		}

		// Состав черновика должен дожить до submit: каждый подписанный товар всё ещё
		// подобран этим оператором (ASSEMBLED с DONE-задачей под это назначение),
		// иначе подпись устарела.
		signedItems := make(map[uuid.UUID]struct{}, len(items))
		for _, id := range items {
			signedItems[id] = struct{}{}
		}
		if len(basket) != len(signedItems) {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer: %w", ErrDraftStale)
		}
		for _, it := range basket {
			if _, ok := signedItems[it.ProductID]; !ok {
				return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer product %s: %w", it.ProductID, ErrDraftStale)
			}
		}

		if err := txRepo.LockSigner(ctx, op.SignerCredentialID); err != nil {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer lock signer: %w", err)
		}

		if err := txRepo.MoveProductsToShippingBuffer(ctx, items, details.ShippingBufferBinID); err != nil {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer move products: %w", err)
		}

		for _, p := range lockedOp.Parts {
			if err := txRepo.StampTaskUnload(ctx, p.ActionID, p.ItemIDs, operatorID, *bufferBin.DestinationID, occurredAt); err != nil {
				return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer stamp tasks of part %s: %w", p.ActionID, err)
			}
		}

		orderIDs := make([]uuid.UUID, 0, len(basket))
		seenOrder := make(map[uuid.UUID]struct{}, len(basket))
		for _, it := range basket {
			// A product whose order was deleted mid-flow still moves to the buffer, but
			// there is no order to promote (matches the old move's IS NOT NULL filter).
			if it.OrderID == nil {
				continue
			}
			if _, ok := seenOrder[*it.OrderID]; ok {
				continue
			}
			seenOrder[*it.OrderID] = struct{}{}
			orderIDs = append(orderIDs, *it.OrderID)
		}
		ordersAssembled, err = txRepo.UpdateOrdersToAssembled(ctx, orderIDs)
		if err != nil {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer update orders: %w", err)
		}

		if err := txRepo.WriteActions(ctx, lockedOp, payloads); err != nil {
			return fmt.Errorf("assembly.Service.SubmitUnloadToShippingBuffer write actions: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &ScanShippingBufferResponse{
		BufferBinID:     bufferBin.BinID.String(),
		ProductsPlaced:  len(items),
		OrdersAssembled: ordersAssembled,
		OperationID:     op.OperationID.String(),
	}, nil
}
