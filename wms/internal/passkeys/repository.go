package passkeys

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
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
