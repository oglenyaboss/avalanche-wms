package passkeys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"wms/internal/domain"
)

// Member is a user with the organization it belongs to (OrgID nil when none).
type Member struct {
	UserID   uuid.UUID
	Username string
	Role     domain.UserRole
	OrgID    *uuid.UUID
	OrgName  string
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetMember returns the user with its organization. OrgID is nil when the user
// belongs to no organization; OrgName is empty in that case.
func (r *Repository) GetMember(ctx context.Context, userID uuid.UUID) (Member, error) {
	var m Member
	var orgName *string
	err := r.db.QueryRow(ctx, `
		SELECT u.user_id, u.username, u.role, u.org_id, o.name
		FROM public.users u
		LEFT JOIN public.organizations o ON o.org_id = u.org_id
		WHERE u.user_id = $1`, userID).Scan(&m.UserID, &m.Username, &m.Role, &m.OrgID, &orgName)
	if err != nil {
		return Member{}, fmt.Errorf("passkeys.Repository.GetMember: %w", err)
	}
	if orgName != nil {
		m.OrgName = *orgName
	}
	return m, nil
}

func (r *Repository) ListOrgMembers(ctx context.Context, orgID uuid.UUID) ([]Member, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.user_id, u.username, u.role, u.org_id
		FROM public.users u
		WHERE u.org_id = $1 AND u.role IN ('ADMIN', 'OPERATOR')
		ORDER BY u.username`, orgID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Repository.ListOrgMembers: %w", err)
	}
	defer rows.Close()

	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.Role, &m.OrgID); err != nil {
			return nil, fmt.Errorf("passkeys.Repository.ListOrgMembers scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("passkeys.Repository.ListOrgMembers rows: %w", err)
	}
	return out, nil
}

func (r *Repository) LatestPasskey(ctx context.Context, userID uuid.UUID) (*domain.Passkey, error) {
	rows, err := r.db.Query(ctx, passkeyColumns+` WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, userID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Repository.LatestPasskey: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("passkeys.Repository.LatestPasskey rows: %w", err)
		}
		return nil, nil
	}
	p, err := scanPasskey(rows)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Repository.LatestPasskey scan: %w", err)
	}
	return &p, nil
}

func (r *Repository) LatestPasskeysByOrg(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]domain.Passkey, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ON (user_id) credential_id, user_id, org_id, qx, qy, key_id, role, status, created_at
		FROM public.passkey_credentials
		WHERE org_id = $1
		ORDER BY user_id, created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Repository.LatestPasskeysByOrg: %w", err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID]domain.Passkey)
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, fmt.Errorf("passkeys.Repository.LatestPasskeysByOrg scan: %w", err)
		}
		out[p.UserID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("passkeys.Repository.LatestPasskeysByOrg rows: %w", err)
	}
	return out, nil
}

func (r *Repository) InsertPasskey(ctx context.Context, p domain.Passkey) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO public.passkey_credentials
			(credential_id, user_id, org_id, qx, qy, key_id, role, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		p.CredentialID, p.UserID, p.OrgID, p.QX[:], p.QY[:], p.KeyID[:], string(p.Role), string(p.Status), p.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("passkeys.Repository.InsertPasskey: %w", ErrPasskeyExists)
		}
		return fmt.Errorf("passkeys.Repository.InsertPasskey exec: %w", err)
	}
	return nil
}

func (r *Repository) UpdatePasskeyStatus(ctx context.Context, credentialID []byte, status domain.PasskeyStatus) error {
	if _, err := r.db.Exec(ctx, `UPDATE public.passkey_credentials SET status = $2 WHERE credential_id = $1`, credentialID, string(status)); err != nil {
		return fmt.Errorf("passkeys.Repository.UpdatePasskeyStatus: %w", err)
	}
	return nil
}

// RejectPendingPasskey sets the passkey with the given credential id to
// REJECTED, but only while it is still PENDING. It reports whether a pending
// key was actually rejected.
func (r *Repository) RejectPendingPasskey(ctx context.Context, credentialID []byte) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE public.passkey_credentials SET status = 'REJECTED' WHERE credential_id = $1 AND status = 'PENDING'`, credentialID)
	if err != nil {
		return false, fmt.Errorf("passkeys.Repository.RejectPendingPasskey: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PasskeyByKeyID returns the passkey with the given registry key id, nil when absent.
func (r *Repository) PasskeyByKeyID(ctx context.Context, keyID [32]byte) (*domain.Passkey, error) {
	return r.passkeyByColumn(ctx, "key_id", keyID[:], "PasskeyByKeyID")
}

// PasskeyByCredentialID returns the passkey with the given credential id, nil when absent.
func (r *Repository) PasskeyByCredentialID(ctx context.Context, id []byte) (*domain.Passkey, error) {
	return r.passkeyByColumn(ctx, "credential_id", id, "PasskeyByCredentialID")
}

func (r *Repository) passkeyByColumn(ctx context.Context, column string, value []byte, op string) (*domain.Passkey, error) {
	rows, err := r.db.Query(ctx, passkeyColumns+` WHERE `+column+` = $1 LIMIT 1`, value)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Repository.%s: %w", op, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("passkeys.Repository.%s rows: %w", op, err)
		}
		return nil, nil
	}
	p, err := scanPasskey(rows)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Repository.%s scan: %w", op, err)
	}
	return &p, nil
}

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
			ka.target_credential_id, ka.action_id, oe.status::text, oe.reject_reason
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
		var status *string
		var reason *int16
		if err := rows.Scan(&credID, &c.ActionID, &status, &reason); err != nil {
			return nil, fmt.Errorf("passkeys.Repository.LatestConfirmations scan: %w", err)
		}
		c.State, c.RejectReason = confirmationState(status, reason)
		out[string(credID)] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("passkeys.Repository.LatestConfirmations rows: %w", err)
	}
	return out, nil
}

func confirmationState(status *string, reason *int16) (ConfirmationState, *int16) {
	if status == nil {
		return ConfirmationSent, nil
	}
	switch *status {
	case "COMMITTED":
		return ConfirmationApplied, nil
	case "FAILED":
		if reason != nil {
			return ConfirmationRejected, reason
		}
		return ConfirmationFailed, nil
	default: // PENDING, SENT
		return ConfirmationSent, nil
	}
}

// SubmitKeyAction submits a drafted key action in one transaction: it locks the
// action, re-checks target and signer status (ADR-0001 §7 serialization), writes
// the signed-action outbox row and marks the draft submitted.
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

	var targetStatus string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM public.passkey_credentials WHERE credential_id = $1 FOR UPDATE`,
		s.TargetCredentialID).Scan(&targetStatus); err != nil {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction lock target: %w", err)
	}
	if targetStatus != string(domain.PasskeyStatusPending) {
		return fmt.Errorf("passkeys.Repository.SubmitKeyAction: %w", ErrTargetNotPending)
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

const passkeyColumns = `SELECT credential_id, user_id, org_id, qx, qy, key_id, role, status, created_at FROM public.passkey_credentials`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPasskey(row rowScanner) (domain.Passkey, error) {
	var p domain.Passkey
	var qx, qy, keyID []byte
	var role, status string
	if err := row.Scan(&p.CredentialID, &p.UserID, &p.OrgID, &qx, &qy, &keyID, &role, &status, &p.CreatedAt); err != nil {
		return domain.Passkey{}, err
	}
	copy(p.QX[:], qx)
	copy(p.QY[:], qy)
	copy(p.KeyID[:], keyID)
	p.Role = domain.UserRole(role)
	p.Status = domain.PasskeyStatus(status)
	return p, nil
}
