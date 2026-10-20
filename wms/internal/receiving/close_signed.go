package receiving

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

// receiveDetails are the draft inputs hashed into every part's detailsHash.
type receiveDetails struct {
	CargoplaceID uuid.UUID `json:"cargoplace_id"`
	OccurredAtMs int64     `json:"occurred_at_ms"`
}

// DraftCloseCargoplace builds the signed Receive operation for closing a
// cargoplace: the closable products are split into parts of at most
// MaxItemsPerAction, each part waits for its own passkey signature.
func (s *Service) DraftCloseCargoplace(ctx context.Context, operatorID, cargoplaceID uuid.UUID) (*signedops.Operation, string, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace: %w", ErrInvalidInput)
	}

	cargoplace, err := s.repo.GetCargoplaceByID(ctx, cargoplaceID)
	if err != nil {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace get cargoplace: %w", err)
	}
	if cargoplace == nil {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace: %w", ErrCargoplaceNotFound)
	}
	if cargoplace.Status != cargoplaceStatusTableInProgress {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace: %w", ErrCargoplaceNotInProgress)
	}

	items, err := s.repo.ListClosableProductIDs(ctx, cargoplaceID)
	if err != nil {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace list items: %w", err)
	}
	if len(items) == 0 {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace: %w", ErrCargoplaceEmpty)
	}

	signer, err := s.signer.ActiveSigner(ctx, operatorID)
	if err != nil {
		return nil, "", err
	}

	// Время обрезается до миллисекунд, иначе detailsHash не воспроизвести из БД (ADR §4).
	occurredAt := time.Now().UTC().Truncate(time.Millisecond)
	details, err := json.Marshal(receiveDetails{CargoplaceID: cargoplaceID, OccurredAtMs: occurredAt.UnixMilli()})
	if err != nil {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace marshal details: %w", err)
	}
	op, err := signedops.NewOperation(signedaction.TypeReceive, operatorID, signer, details, items,
		func(salt [32]byte, _ []uuid.UUID) [32]byte {
			return signedaction.ReceiveDetailsHash(salt, cargoplaceID, operatorID, uint64(occurredAt.UnixMilli()))
		})
	if err != nil {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace new operation: %w", err)
	}

	if err := s.ops.InsertOperation(ctx, op); err != nil {
		return nil, "", fmt.Errorf("receiving.Service.DraftCloseCargoplace insert: %w", err)
	}
	rpID, _ := s.signer.RelyingParty()
	return op, rpID, nil
}

// SubmitCloseCargoplace verifies the operator's signatures over every part and
// applies the whole operation in one repository transaction. Signature errors
// are returned as is: nothing is written before every part verifies.
func (s *Service) SubmitCloseCargoplace(ctx context.Context, operatorID, operationID uuid.UUID, assertions map[uuid.UUID]signedaction.Assertion) (*CloseCargoplaceResult, error) {
	op, err := s.ops.Operation(ctx, operationID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.SubmitCloseCargoplace load: %w", err)
	}
	if op == nil || op.CreatedBy != operatorID || op.ActionType != signedaction.TypeReceive {
		return nil, fmt.Errorf("receiving.Service.SubmitCloseCargoplace: %w", signedops.ErrOperationNotFound)
	}
	if op.Submitted {
		return nil, fmt.Errorf("receiving.Service.SubmitCloseCargoplace: %w", signedops.ErrOperationSubmitted)
	}

	signer, err := s.signer.ActiveSigner(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(signer.CredentialID, op.SignerCredentialID) {
		return nil, fmt.Errorf("receiving.Service.SubmitCloseCargoplace: %w", signedops.ErrSignerNotActive)
	}

	rpID, origins := s.signer.RelyingParty()
	payloads, err := signedops.Envelopes(op, signer, assertions, rpID, origins)
	if err != nil {
		return nil, err
	}

	var details receiveDetails
	if err := json.Unmarshal(op.Details, &details); err != nil {
		return nil, fmt.Errorf("receiving.Service.SubmitCloseCargoplace decode details: %w", err)
	}
	occurredAt := time.UnixMilli(details.OccurredAtMs).UTC()

	txResult, err := s.repo.CloseCargoplaceWithOutbox(ctx, &CloseCargoplaceParams{
		CargoplaceID: details.CargoplaceID,
		OperatorID:   operatorID,
		OccurredAt:   occurredAt,
		OperationID:  &op.OperationID,
		Payloads:     payloads,
	})
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.SubmitCloseCargoplace close cargoplace with outbox: %w", err)
	}

	summary := buildCloseCargoplaceSummary(txResult.ExpectedSKUs, txResult.ReceivedSKUCounts)
	return &CloseCargoplaceResult{
		CargoplaceID:        details.CargoplaceID,
		OperationID:         op.OperationID,
		Status:              cargoplaceStatusTableClosed,
		Summary:             summary,
		OutboxEventsCreated: len(payloads),
	}, nil
}
