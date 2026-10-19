package passkeys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wms/internal/domain"
	"wms/internal/signedaction"
)

// InsertKeyAction stores a draft of a signed key action.
func (r *Repository) InsertKeyAction(ctx context.Context, a KeyAction) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO public.key_actions
			(action_id, org_id, action_type, target_credential_id, signer_credential_id, challenge, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.ActionID, a.OrgID, int16(a.ActionType), a.TargetCredentialID, a.SignerCredentialID, a.Challenge[:], a.CreatedBy)
	if err != nil {
		return fmt.Errorf("passkeys.Repository.InsertKeyAction: %w", err)
	}
	return nil
}

// KeyAction returns the stored key action, nil when absent.
func (r *Repository) KeyAction(ctx context.Context, actionID uuid.UUID) (*KeyAction, error) {
	var a KeyAction
	var actionType int16
	var challenge []byte
	err := r.db.QueryRow(ctx, `
		SELECT action_id, org_id, action_type, target_credential_id, signer_credential_id, challenge, created_by, submitted_at
		FROM public.key_actions WHERE action_id = $1`, actionID).
		Scan(&a.ActionID, &a.OrgID, &actionType, &a.TargetCredentialID, &a.SignerCredentialID, &challenge, &a.CreatedBy, &a.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("passkeys.Repository.KeyAction: %w", err)
	}
	a.ActionType = uint8(actionType)
	copy(a.Challenge[:], challenge)
	return &a, nil
}

// LatestConfirmations maps the latest submitted key action of each target key in
// the organization to its derived chain confirmation state.
func (r *Repository) LatestConfirmations(ctx context.Context, orgID uuid.UUID) (map[string]KeyConfirmation, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ON (ka.target_credential_id)
			ka.target_credential_id, ka.action_id, ka.action_type, oe.status::text, oe.reject_reason
		FROM public.key_actions ka
		LEFT JOIN public.onchain_events oe ON oe.event_id = ka.action_id
		WHERE ka.org_id = $1 AND ka.submitted_at IS NOT NULL
		ORDER BY ka.target_credential_id, ka.submitted_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Repository.LatestConfirmations: %w", err)
	}
	defer rows.Close()

	out := make(map[string]KeyConfirmation)
	for rows.Next() {
		var credID []byte
		var c KeyConfirmation
		var actionType int16
		var status *string
		var reason *int16
		if err := rows.Scan(&credID, &c.ActionID, &actionType, &status, &reason); err != nil {
			return nil, fmt.Errorf("passkeys.Repository.LatestConfirmations scan: %w", err)
		}
		c.ActionType = uint8(actionType)
		c.State, c.RejectReason = signedaction.ConfirmationStateOf(status, reason)
		out[string(credID)] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("passkeys.Repository.LatestConfirmations rows: %w", err)
	}
	return out, nil
}

// keyActionInFlight returns the action type and the derived chain confirmation
// state of the latest submitted key action for the target credential; found is
// false when there is none. Must be called inside the transaction that holds
// the target row lock.
func keyActionInFlight(ctx context.Context, q pgx.Tx, targetCredentialID []byte) (uint8, signedaction.ConfirmationState, bool, error) {
	var actionType int16
	var status *string
	var reason *int16
	err := q.QueryRow(ctx, `
		SELECT ka.action_type, oe.status::text, oe.reject_reason
		FROM public.key_actions ka
		LEFT JOIN public.onchain_events oe ON oe.event_id = ka.action_id
		WHERE ka.target_credential_id = $1 AND ka.submitted_at IS NOT NULL
		ORDER BY ka.submitted_at DESC LIMIT 1`, targetCredentialID).Scan(&actionType, &status, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("passkeys.Repository.keyActionInFlight: %w", err)
	}
	state, _ := signedaction.ConfirmationStateOf(status, reason)
	return uint8(actionType), state, true, nil
}

// SubmitKeyAction submits a drafted key action in one transaction: it locks the
// action, re-checks target and signer status and that no other action for the
// target is in flight (ADR-0001 §7 serialization), writes the signed-action
// outbox row and marks the draft submitted.
// ВНИМАНИЕ: метод сам управляет транзакцией через r.db.Begin — НЕ оборачивать в WithTx.
func (r *Repository) SubmitKeyAction(ctx context.Context, s KeyActionSubmission) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var submittedAt *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT submitted_at FROM public.key_actions WHERE action_id = $1 FOR UPDATE`,
		s.ActionID).Scan(&submittedAt); err != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction lock action: %w", err)
	}
	if submittedAt != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction: %w", ErrActionAlreadySubmitted)
	}

	// KeyRegister подтверждает PENDING ключ, KeyRevoke отзывает ACTIVE;
	// ожидаемый статус целевого ключа зависит от типа действия.
	wantStatus, wrongStatus := domain.PasskeyStatusPending, error(ErrTargetNotPending)
	if s.ActionType == signedaction.TypeKeyRevoke {
		wantStatus, wrongStatus = domain.PasskeyStatusActive, ErrTargetNotActive
	}
	var targetStatus string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM public.passkey_credentials WHERE credential_id = $1 FOR UPDATE`,
		s.TargetCredentialID).Scan(&targetStatus); err != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction lock target: %w", err)
	}
	if targetStatus != string(wantStatus) {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction: %w", wrongStatus)
	}

	lastType, state, found, err := keyActionInFlight(ctx, tx, s.TargetCredentialID)
	if err != nil {
		return err
	}
	// KeyRegister блокируется действием в работе или уже применённым;
	// KeyRevoke — только действием в работе или уже применённым отзывом.
	inFlight := found && (state == signedaction.ConfirmationSent ||
		(state == signedaction.ConfirmationApplied &&
			(s.ActionType == signedaction.TypeKeyRegister || lastType == signedaction.TypeKeyRevoke)))
	if inFlight {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction: %w", ErrActionInProgress)
	}

	var signerStatus string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM public.passkey_credentials WHERE credential_id = $1 FOR SHARE`,
		s.SignerCredentialID).Scan(&signerStatus); err != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction lock signer: %w", err)
	}
	if signerStatus != string(domain.PasskeyStatusActive) {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction: %w", ErrSignerKeyNotActive)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash, payload)
		VALUES ($1, $1, 'signed_action', 'wms.action.v1', $2, $3::jsonb)`,
		s.ActionID, s.PayloadHash, string(s.Payload)); err != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction insert outbox: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE public.key_actions SET submitted_at = now() WHERE action_id = $1`,
		s.ActionID); err != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction mark submitted: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction commit tx: %w", err)
	}
	return nil
}

// RejectPendingPasskey rejects the passkey with the given credential id in one
// transaction: it locks the key row, re-checks the PENDING status and that no
// key action for this key is in flight, then sets the status to REJECTED.
// ВНИМАНИЕ: метод сам управляет транзакцией через r.db.Begin — НЕ оборачивать в WithTx.
func (r *Repository) RejectPendingPasskey(ctx context.Context, credentialID []byte) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("passkeys.Repository.RejectPendingPasskey begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM public.passkey_credentials WHERE credential_id = $1 FOR UPDATE`,
		credentialID).Scan(&status); err != nil {
		return fmt.Errorf("passkeys.Repository.RejectPendingPasskey lock target: %w", err)
	}
	if status != string(domain.PasskeyStatusPending) {
		return fmt.Errorf("passkeys.Repository.RejectPendingPasskey: %w", ErrTargetNotPending)
	}

	_, state, found, err := keyActionInFlight(ctx, tx, credentialID)
	if err != nil {
		return err
	}
	if found && (state == signedaction.ConfirmationSent || state == signedaction.ConfirmationApplied) {
		return fmt.Errorf("passkeys.Repository.RejectPendingPasskey: %w", ErrActionInProgress)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE public.passkey_credentials SET status = 'REJECTED' WHERE credential_id = $1`,
		credentialID); err != nil {
		return fmt.Errorf("passkeys.Repository.RejectPendingPasskey update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("passkeys.Repository.RejectPendingPasskey commit tx: %w", err)
	}
	return nil
}
