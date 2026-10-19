package signedops

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wms/internal/domain"
	"wms/internal/signedaction"
)

// Repository stores warehouse action operations in public.warehouse_actions.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const operationColumns = `action_id, action_type, signer_credential_id, created_by, item_ids, salt, details_hash, challenge, details, submitted_at`

// PartStatus is the derived chain confirmation of one submitted part.
type PartStatus struct {
	ActionID     uuid.UUID
	State        signedaction.ConfirmationState
	RejectReason *int16
}

// InsertOperation stores every part of the operation in one transaction.
func (r *Repository) InsertOperation(ctx context.Context, op *Operation) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("signedops.Repository.InsertOperation begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	for i, p := range op.Parts {
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.warehouse_actions
				(action_id, operation_id, part_index, action_type, signer_credential_id, created_by, item_ids, salt, details_hash, challenge, details)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)`,
			p.ActionID, op.OperationID, int16(i), int16(op.ActionType), op.SignerCredentialID, op.CreatedBy, p.ItemIDs, p.Salt[:], p.DetailsHash[:], p.Challenge[:], string(op.Details)); err != nil {
			return fmt.Errorf("signedops.Repository.InsertOperation part %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("signedops.Repository.InsertOperation commit tx: %w", err)
	}
	return nil
}

// Operation loads the operation with its parts in part_index order,
// nil when the operation has no rows.
func (r *Repository) Operation(ctx context.Context, operationID uuid.UUID) (*Operation, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+operationColumns+`
		FROM public.warehouse_actions
		WHERE operation_id = $1
		ORDER BY part_index`, operationID)
	if err != nil {
		return nil, fmt.Errorf("signedops.Repository.Operation: %w", err)
	}
	defer rows.Close()
	return scanOperation(rows, operationID)
}

// OperationStatus returns the chain confirmation of every part of the caller's
// operation. The whole operation must be submitted: parts await confirmation
// only together.
func (r *Repository) OperationStatus(ctx context.Context, operationID, userID uuid.UUID) ([]PartStatus, error) {
	rows, err := r.db.Query(ctx, `
		SELECT wa.action_id, wa.submitted_at, oe.status::text, oe.reject_reason
		FROM public.warehouse_actions wa
		LEFT JOIN public.onchain_events oe ON oe.event_id = wa.action_id
		WHERE wa.operation_id = $1 AND wa.created_by = $2
		ORDER BY wa.part_index`, operationID, userID)
	if err != nil {
		return nil, fmt.Errorf("signedops.Repository.OperationStatus: %w", err)
	}
	defer rows.Close()

	var out []PartStatus
	unsubmitted := false
	for rows.Next() {
		var ps PartStatus
		var submittedAt *time.Time
		var status *string
		if err := rows.Scan(&ps.ActionID, &submittedAt, &status, &ps.RejectReason); err != nil {
			return nil, fmt.Errorf("signedops.Repository.OperationStatus scan: %w", err)
		}
		if submittedAt == nil {
			unsubmitted = true
		}
		ps.State, ps.RejectReason = signedaction.ConfirmationStateOf(status, ps.RejectReason)
		out = append(out, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signedops.Repository.OperationStatus rows: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("signedops.Repository.OperationStatus: %w", ErrOperationNotFound)
	}
	if unsubmitted {
		return nil, fmt.Errorf("signedops.Repository.OperationStatus: %w", ErrOperationNotSubmitted)
	}
	return out, nil
}

// LockOperation locks every part of the caller's operation FOR UPDATE in
// part_index order and returns it; no rows or a foreign creator means the
// operation does not exist for this caller.
func LockOperation(ctx context.Context, tx pgx.Tx, operationID, createdBy uuid.UUID) (*Operation, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+operationColumns+`
		FROM public.warehouse_actions
		WHERE operation_id = $1 AND created_by = $2
		ORDER BY part_index
		FOR UPDATE`, operationID, createdBy)
	if err != nil {
		return nil, fmt.Errorf("signedops.LockOperation: %w", err)
	}
	defer rows.Close()
	op, err := scanOperation(rows, operationID)
	if err != nil {
		return nil, err
	}
	if op == nil {
		return nil, fmt.Errorf("signedops.LockOperation: %w", ErrOperationNotFound)
	}
	if op.Submitted {
		return nil, fmt.Errorf("signedops.LockOperation: %w", ErrOperationSubmitted)
	}
	return op, nil
}

// LockSigner locks the signer's passkey row FOR SHARE and requires it to be
// ACTIVE, serializing against key revocation (ADR §7).
func LockSigner(ctx context.Context, tx pgx.Tx, credentialID []byte) error {
	var status string
	err := tx.QueryRow(ctx, `
		SELECT status FROM public.passkey_credentials WHERE credential_id = $1 FOR SHARE`,
		credentialID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("signedops.LockSigner: %w", ErrSignerNotActive)
	}
	if err != nil {
		return fmt.Errorf("signedops.LockSigner: %w", err)
	}
	if status != string(domain.PasskeyStatusActive) {
		return fmt.Errorf("signedops.LockSigner: %w", ErrSignerNotActive)
	}
	return nil
}

// WriteActions writes one outbox row per part, in part order, and marks the
// whole operation submitted. The caller owns the transaction.
func WriteActions(ctx context.Context, tx pgx.Tx, op *Operation, payloads [][]byte) error {
	if len(payloads) != len(op.Parts) {
		return fmt.Errorf("signedops.WriteActions: %d payloads for %d parts", len(payloads), len(op.Parts))
	}
	for i, p := range op.Parts {
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash, payload)
			VALUES ($1, $1, 'signed_action', 'wms.action.v1', $2, $3::jsonb)`,
			p.ActionID, hex.EncodeToString(p.Challenge[:]), string(payloads[i])); err != nil {
			return fmt.Errorf("signedops.WriteActions insert outbox part %d: %w", i, err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE public.warehouse_actions SET submitted_at = now() WHERE operation_id = $1`,
		op.OperationID); err != nil {
		return fmt.Errorf("signedops.WriteActions mark submitted: %w", err)
	}
	return nil
}

// scanOperation folds the part rows of one operation into an Operation,
// nil when there are no rows.
func scanOperation(rows pgx.Rows, operationID uuid.UUID) (*Operation, error) {
	op := &Operation{OperationID: operationID}
	for rows.Next() {
		var p Part
		var actionType int16
		var salt, detailsHash, challenge, details []byte
		var submittedAt *time.Time
		if err := rows.Scan(&p.ActionID, &actionType, &op.SignerCredentialID, &op.CreatedBy, &p.ItemIDs, &salt, &detailsHash, &challenge, &details, &submittedAt); err != nil {
			return nil, fmt.Errorf("signedops.scanOperation: %w", err)
		}
		op.ActionType = uint8(actionType)
		copy(p.Salt[:], salt)
		copy(p.DetailsHash[:], detailsHash)
		copy(p.Challenge[:], challenge)
		op.Details = details
		if submittedAt != nil {
			op.Submitted = true
		}
		op.Parts = append(op.Parts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signedops.scanOperation rows: %w", err)
	}
	if len(op.Parts) == 0 {
		return nil, nil
	}
	return op, nil
}
