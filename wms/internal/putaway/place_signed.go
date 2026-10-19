package putaway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/ledger"
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

// putawayDetails are the draft inputs hashed into every part's detailsHash.
type putawayDetails struct {
	StorageBinID uuid.UUID `json:"storage_bin_id"`
	OccurredAtMs int64     `json:"occurred_at_ms"`
}

// DraftPlaceToStorageBin builds the signed PutAway operation for placing a
// basket of buffer products into a storage bin: the basket is deduplicated,
// every product is verified read-only (exists, sits in a BUFFER bin, RECEIVED)
// and split into parts of at most MaxItemsPerAction, each part waits for its
// own passkey signature. Nothing is written except the draft itself.
func (s *Service) DraftPlaceToStorageBin(ctx context.Context, operatorID uuid.UUID, productIDs []uuid.UUID, storageBinID uuid.UUID) (*signedops.Operation, string, error) {
	if operatorID == uuid.Nil || storageBinID == uuid.Nil || len(productIDs) == 0 {
		return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin: %w", ErrInvalidInput)
	}

	if _, err := s.repo.GetStorageBinByID(ctx, storageBinID); err != nil {
		return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin: %w", err)
	}

	// Дубликаты корзины схлопываются: один товар нельзя разместить дважды одним действием.
	items := make([]uuid.UUID, 0, len(productIDs))
	seen := make(map[uuid.UUID]struct{}, len(productIDs))
	for _, id := range productIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		items = append(items, id)
	}

	verifiedBuffers := make(map[uuid.UUID]struct{})
	for _, productID := range items {
		product, err := s.repo.GetProductByID(ctx, productID)
		if err != nil {
			return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin get product %s: %w", productID, err)
		}
		if product.BinID == nil {
			return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin product %s: %w", productID, ErrProductNotInBuffer)
		}
		if _, ok := verifiedBuffers[*product.BinID]; !ok {
			if _, err := s.repo.GetBufferBinByID(ctx, *product.BinID); err != nil {
				if errors.Is(err, ErrBufferBinNotFound) {
					return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin product %s: %w", productID, ErrProductNotInBuffer)
				}
				return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin verify buffer for product %s: %w", productID, err)
			}
			verifiedBuffers[*product.BinID] = struct{}{}
		}
		if product.Status != "RECEIVED" {
			return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin product %s: %w", productID, ErrProductNotReceived)
		}
	}

	// #45: gate on the previous FSM stage. Reject placement if any product's receiving
	// event is FAILED on-chain — the WMS must not advance a product whose prior stage
	// diverged from the ledger. Read-only pre-check.
	if err := s.repo.CheckChainStatus(ctx, items, ledger.AggregateReceiving); err != nil {
		return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin: %w", err)
	}

	signer, err := s.signer.ActiveSigner(ctx, operatorID)
	if err != nil {
		return nil, "", err
	}

	// Время обрезается до миллисекунд, иначе detailsHash не воспроизвести из БД (ADR §4).
	occurredAt := time.Now().UTC().Truncate(time.Millisecond)
	details, err := json.Marshal(putawayDetails{StorageBinID: storageBinID, OccurredAtMs: occurredAt.UnixMilli()})
	if err != nil {
		return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin marshal details: %w", err)
	}
	op, err := signedops.NewOperation(signedaction.TypePutAway, operatorID, signer, details, items,
		func(salt [32]byte, _ []uuid.UUID) [32]byte {
			return signedaction.PutAwayDetailsHash(salt, storageBinID, operatorID, uint64(occurredAt.UnixMilli()))
		})
	if err != nil {
		return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin new operation: %w", err)
	}

	if err := s.ops.InsertOperation(ctx, op); err != nil {
		return nil, "", fmt.Errorf("putaway.Service.DraftPlaceToStorageBin insert: %w", err)
	}
	rpID, _ := s.signer.RelyingParty()
	return op, rpID, nil
}

// SubmitPlaceToStorageBin verifies the operator's signatures over every part and
// applies the whole operation in one repository transaction. Signature errors
// are returned as is: nothing is written before every part verifies.
func (s *Service) SubmitPlaceToStorageBin(ctx context.Context, operatorID, operationID uuid.UUID, assertions map[uuid.UUID]signedaction.Assertion) (*ScanStorageBinResponse, error) {
	op, err := s.ops.Operation(ctx, operationID)
	if err != nil {
		return nil, fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin load: %w", err)
	}
	if op == nil || op.CreatedBy != operatorID || op.ActionType != signedaction.TypePutAway {
		return nil, fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin: %w", signedops.ErrOperationNotFound)
	}
	if op.Submitted {
		return nil, fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin: %w", signedops.ErrOperationSubmitted)
	}

	signer, err := s.signer.ActiveSigner(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(signer.CredentialID, op.SignerCredentialID) {
		return nil, fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin: %w", signedops.ErrSignerNotActive)
	}

	rpID, origins := s.signer.RelyingParty()
	payloads, err := signedops.Envelopes(op, signer, assertions, rpID, origins)
	if err != nil {
		return nil, err
	}

	var details putawayDetails
	if err := json.Unmarshal(op.Details, &details); err != nil {
		return nil, fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin decode details: %w", err)
	}
	occurredAt := time.UnixMilli(details.OccurredAtMs).UTC()

	items := make([]uuid.UUID, 0, len(op.Parts))
	for _, p := range op.Parts {
		items = append(items, p.ItemIDs...)
	}

	// #45: re-run the gate on the signed set at submit time — between draft and
	// submit a receiving event could turn FAILED on-chain.
	if err := s.repo.CheckChainStatus(ctx, items, ledger.AggregateReceiving); err != nil {
		return nil, fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin: %w", err)
	}

	var bin *domain.Bin
	err = s.repo.WithTx(ctx, func(txRepo putawayRepository) error {
		var err error
		bin, err = txRepo.GetStorageBinByID(ctx, details.StorageBinID)
		if err != nil {
			return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin: %w", err)
		}

		// ADR §7: сначала блокируем строки товаров FOR UPDATE в детерминированном
		// порядке (по возрастанию product_id), затем операцию и ключ подписанта.
		sorted := make([]uuid.UUID, len(items))
		copy(sorted, items)
		sort.Slice(sorted, func(i, j int) bool {
			return bytes.Compare(sorted[i][:], sorted[j][:]) < 0
		})
		locked := make([]*domain.Product, 0, len(sorted))
		for _, productID := range sorted {
			product, err := txRepo.GetProductsByIDForUpdate(ctx, productID)
			if err != nil {
				if errors.Is(err, ErrProductNotFound) {
					return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin product %s: %w", productID, ErrDraftStale)
				}
				return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin get product %s: %w", productID, err)
			}
			locked = append(locked, product)
		}

		lockedOp, err := txRepo.LockOperation(ctx, operationID, operatorID)
		if err != nil {
			return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin lock operation: %w", err)
		}

		// Состав черновика должен дожить до submit: каждый подписанный товар всё ещё
		// лежит в буферной ячейке со статусом RECEIVED, иначе подпись устарела.
		verifiedBuffers := make(map[uuid.UUID]struct{})
		for _, product := range locked {
			if product.BinID == nil {
				return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin product %s: %w", product.ProductID, ErrDraftStale)
			}
			if _, ok := verifiedBuffers[*product.BinID]; !ok {
				if _, err := txRepo.GetBufferBinByID(ctx, *product.BinID); err != nil {
					if errors.Is(err, ErrBufferBinNotFound) {
						return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin product %s: %w", product.ProductID, ErrDraftStale)
					}
					return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin verify buffer for product %s: %w", product.ProductID, err)
				}
				verifiedBuffers[*product.BinID] = struct{}{}
			}
			if product.Status != "RECEIVED" {
				return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin product %s: %w", product.ProductID, ErrDraftStale)
			}
		}

		if err := txRepo.LockSigner(ctx, op.SignerCredentialID); err != nil {
			return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin lock signer: %w", err)
		}

		partByProduct := make(map[uuid.UUID]uuid.UUID, len(items))
		for _, p := range lockedOp.Parts {
			for _, id := range p.ItemIDs {
				partByProduct[id] = p.ActionID
			}
		}

		for _, product := range locked {
			if err := txRepo.UpdateProductStorage(ctx, product.ProductID, details.StorageBinID); err != nil {
				return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin update product %s: %w", product.ProductID, err)
			}
			if err := txRepo.InsertPutaway(ctx, &InsertPutawayParams{
				EventID:    uuid.New(),
				ProductID:  product.ProductID,
				FromBinID:  *product.BinID,
				BinID:      details.StorageBinID,
				OperatorID: operatorID,
				OccurredAt: occurredAt,
				ActionID:   partByProduct[product.ProductID],
			}); err != nil {
				return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin insert putaway: %w", err)
			}
		}

		if err := txRepo.WriteActions(ctx, lockedOp, payloads); err != nil {
			return fmt.Errorf("putaway.Service.SubmitPlaceToStorageBin write actions: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &ScanStorageBinResponse{
		StorageBinID:        bin.BinID.String(),
		StorageBinCode:      bin.Code,
		ProductsPlaced:      len(items),
		OutboxEventsCreated: len(op.Parts),
		OperationID:         op.OperationID.String(),
	}, nil
}
