package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrProductNotFound is wrapped by ProductID when the key matches no product.
var ErrProductNotFound = errors.New("product not found")

// ErrDomainRowMissing is wrapped by Details when the domain row(s) behind the action are absent.
var ErrDomainRowMissing = errors.New("domain row missing")

// ErrDomainInconsistent is wrapped by Details when the domain rows disagree on a single detail value.
var ErrDomainInconsistent = errors.New("domain rows disagree")

// Action is a submitted signed warehouse action as WMS stored it.
type Action struct {
	ActionID     uuid.UUID
	OperationID  uuid.UUID
	PartIndex    int16
	ActionType   uint8
	ItemIDs      []uuid.UUID
	Salt         [32]byte
	SubmittedAt  time.Time
	OperatorID   uuid.UUID // warehouse_actions.created_by
	Operator     string    // users.username
	OrgID        uuid.UUID // passkey_credentials.org_id of the signer credential
	OrgName      string
	KeyID        [32]byte // passkey_credentials.key_id of the signer credential
	ChainStatus  string   // onchain_events.status, "" when there is no row
	RejectReason *int16
}

// Repository reads submitted signed warehouse actions and the domain rows they changed.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a Repository over the pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const actionSelect = `
	SELECT wa.action_id, wa.operation_id, wa.part_index, wa.action_type, wa.item_ids, wa.salt,
	       wa.submitted_at, wa.created_by, u.username, pc.org_id, o.name, pc.key_id,
	       oe.status::text, oe.reject_reason
	FROM public.warehouse_actions wa
	JOIN public.passkey_credentials pc ON pc.credential_id = wa.signer_credential_id
	JOIN public.organizations o ON o.org_id = pc.org_id
	JOIN public.users u ON u.user_id = wa.created_by
	LEFT JOIN public.onchain_events oe ON oe.event_id = wa.action_id`

// ProductID resolves a product uuid or qr_code to the product_id.
func (r *Repository) ProductID(ctx context.Context, key string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT product_id
		FROM wms_inventory.products
		WHERE qr_code = $1 OR product_id::text = $1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, fmt.Errorf("audit.Repository.ProductID: %w", ErrProductNotFound)
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("audit.Repository.ProductID: %w", err)
	}
	return id, nil
}

// ActionsForProduct returns every submitted action whose item_ids contain the product,
// ordered by submitted_at, part_index.
func (r *Repository) ActionsForProduct(ctx context.Context, productID uuid.UUID) ([]Action, error) {
	rows, err := r.db.Query(ctx, actionSelect+`
	WHERE wa.submitted_at IS NOT NULL AND wa.item_ids @> ARRAY[$1::uuid]
	ORDER BY wa.submitted_at, wa.part_index`, productID)
	if err != nil {
		return nil, fmt.Errorf("audit.Repository.ActionsForProduct: %w", err)
	}
	defer rows.Close()
	return scanActions(rows)
}

// Action returns the submitted action, nil when it is absent or not submitted.
func (r *Repository) Action(ctx context.Context, actionID uuid.UUID) (*Action, error) {
	rows, err := r.db.Query(ctx, actionSelect+`
	WHERE wa.action_id = $1 AND wa.submitted_at IS NOT NULL`, actionID)
	if err != nil {
		return nil, fmt.Errorf("audit.Repository.Action: %w", err)
	}
	defer rows.Close()
	actions, err := scanActions(rows)
	if err != nil {
		return nil, fmt.Errorf("audit.Repository.Action: %w", err)
	}
	if len(actions) == 0 {
		return nil, nil
	}
	return &actions[0], nil
}

// AllActions returns every submitted action, ordered by submitted_at, part_index.
func (r *Repository) AllActions(ctx context.Context) ([]Action, error) {
	rows, err := r.db.Query(ctx, actionSelect+`
	WHERE wa.submitted_at IS NOT NULL
	ORDER BY wa.submitted_at, wa.part_index`)
	if err != nil {
		return nil, fmt.Errorf("audit.Repository.AllActions: %w", err)
	}
	defer rows.Close()
	return scanActions(rows)
}

func scanActions(rows pgx.Rows) ([]Action, error) {
	var out []Action
	for rows.Next() {
		var a Action
		var actionType int16
		var salt, keyID []byte
		var submittedAt *time.Time
		var status *string
		if err := rows.Scan(&a.ActionID, &a.OperationID, &a.PartIndex, &actionType, &a.ItemIDs, &salt,
			&submittedAt, &a.OperatorID, &a.Operator, &a.OrgID, &a.OrgName, &keyID, &status, &a.RejectReason); err != nil {
			return nil, fmt.Errorf("audit.scanActions: %w", err)
		}
		a.ActionType = uint8(actionType)
		salt32, err := fixed32(salt)
		if err != nil {
			return nil, fmt.Errorf("audit.scanActions: %w", err)
		}
		a.Salt = salt32
		key, err := fixed32(keyID)
		if err != nil {
			return nil, fmt.Errorf("audit.scanActions: %w", err)
		}
		a.KeyID = key
		if submittedAt != nil {
			a.SubmittedAt = *submittedAt
		}
		if status != nil {
			a.ChainStatus = *status
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit.scanActions rows: %w", err)
	}
	return out, nil
}

func fixed32(b []byte) ([32]byte, error) {
	var out [32]byte
	if len(b) != 32 {
		return out, fmt.Errorf("audit.fixed32: got %d bytes, want 32", len(b))
	}
	copy(out[:], b)
	return out, nil
}
