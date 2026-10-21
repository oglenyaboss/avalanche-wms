package audit

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
)

// Check is the outcome of one stage check.
type Check string

// Check outcomes of a stage.
const (
	CheckOK       Check = "OK"
	CheckMismatch Check = "MISMATCH"
	CheckSkipped  Check = "SKIPPED"
)

// Status is the on-chain status of a stage.
type Status string

// Stage statuses.
const (
	StatusApplied        Status = "APPLIED"
	StatusRejected       Status = "REJECTED"
	StatusPending        Status = "PENDING"
	StatusMissingOnChain Status = "MISSING_ON_CHAIN"
	StatusMissingInDB    Status = "MISSING_IN_DB"
)

// Kind classifies a finding.
type Kind string

// Finding kinds.
const (
	KindDetailsHash    Kind = "DETAILS_HASH_MISMATCH"
	KindItems          Kind = "ITEMS_MISMATCH"
	KindSigner         Kind = "SIGNER_MISMATCH"
	KindSignature      Kind = "BAD_SIGNATURE"
	KindType           Kind = "TYPE_MISMATCH"
	KindMissingInDB    Kind = "MISSING_IN_DB"
	KindMissingOnChain Kind = "MISSING_ON_CHAIN"
)

// Finding is one concrete mismatch of the reconciliation.
type Finding struct {
	ActionID uuid.UUID `json:"actionId"`
	Kind     Kind      `json:"kind"`
	Detail   string    `json:"detail"`
}

// Stage is the reconciliation result of one action.
type Stage struct {
	ActionID     uuid.UUID  `json:"actionId"`
	ActionType   uint8      `json:"actionType"`
	Status       Status     `json:"status"`
	OperatorID   uuid.UUID  `json:"operatorId"`
	Operator     string     `json:"operator"`
	OrgID        uuid.UUID  `json:"orgId"`
	OrgName      string     `json:"orgName"`
	KeyID        string     `json:"keyId"`
	BlockNumber  uint64     `json:"blockNumber"`
	BlockTime    *time.Time `json:"blockTime"`
	TxHash       string     `json:"txHash"`
	RejectReason uint8      `json:"rejectReason"`
	DetailsHash  Check      `json:"detailsHash"`
	Items        Check      `json:"items"`
	Signer       Check      `json:"signer"`
	Signature    Check      `json:"signature"`
	Findings     []Finding  `json:"findings"`
}

// ProductReport is the reconciliation of every action that touched one product.
type ProductReport struct {
	ProductID uuid.UUID `json:"productId"`
	Stages    []Stage   `json:"stages"`
}

// Findings flattens the findings of all stages.
func (r ProductReport) Findings() []Finding {
	var out []Finding
	for _, s := range r.Stages {
		out = append(out, s.Findings...)
	}
	return out
}

type store interface {
	ActionsForProduct(ctx context.Context, productID uuid.UUID) ([]Action, error)
	Action(ctx context.Context, actionID uuid.UUID) (*Action, error)
	AllActions(ctx context.Context) ([]Action, error)
	Details(ctx context.Context, a Action) (Details, error)
	DomainItems(ctx context.Context, a Action) ([]uuid.UUID, error)
}

type ledger interface {
	ActionIDsForItem(ctx context.Context, productID uuid.UUID) ([]uuid.UUID, error)
	ItemsOfAction(ctx context.Context, actionID uuid.UUID) ([][32]byte, error)
	Applied(ctx context.Context, actionIDs []uuid.UUID) (map[uuid.UUID]ChainAction, error)
	Rejected(ctx context.Context, actionIDs []uuid.UUID) (map[uuid.UUID]ChainRejection, error)
	Key(ctx context.Context, keyID [32]byte) (Key, error)
	Auth(ctx context.Context, tx common.Hash, actionID uuid.UUID) (OnChainAuth, error)
}

// Auditor compares submitted signed actions in the DB with the chain and the signatures.
type Auditor struct {
	store  store
	ledger ledger
}

// NewAuditor builds an Auditor over the DB store and the chain reader.
func NewAuditor(s store, l ledger) *Auditor {
	return &Auditor{store: s, ledger: l}
}

// Product reconciles every action that touched the product, ordered by block
// time when applied and by DB submission time otherwise.
func (a *Auditor) Product(ctx context.Context, productID uuid.UUID) (ProductReport, error) {
	chainIDs, err := a.ledger.ActionIDsForItem(ctx, productID)
	if err != nil {
		return ProductReport{}, fmt.Errorf("audit.Auditor.Product: %w", err)
	}
	dbActions, err := a.store.ActionsForProduct(ctx, productID)
	if err != nil {
		return ProductReport{}, fmt.Errorf("audit.Auditor.Product: %w", err)
	}
	ids, dbBy := union(chainIDs, dbActions)
	applied, err := a.ledger.Applied(ctx, ids)
	if err != nil {
		return ProductReport{}, fmt.Errorf("audit.Auditor.Product: %w", err)
	}
	rejected, err := a.ledger.Rejected(ctx, ids)
	if err != nil {
		return ProductReport{}, fmt.Errorf("audit.Auditor.Product: %w", err)
	}

	type item struct {
		at    time.Time
		stage Stage
	}
	items := make([]item, 0, len(ids))
	for _, id := range ids {
		db := dbBy[id]
		var ca *ChainAction
		if v, ok := applied[id]; ok {
			ca = &v
		}
		var cr *ChainRejection
		if v, ok := rejected[id]; ok {
			cr = &v
		}
		s, err := a.stage(ctx, db, ca, cr)
		if err != nil {
			return ProductReport{}, fmt.Errorf("audit.Auditor.Product: %w", err)
		}
		at := time.Time{}
		if ca != nil {
			at = ca.BlockTime
		} else if db != nil {
			at = db.SubmittedAt
		}
		items = append(items, item{at: at, stage: s})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })

	stages := make([]Stage, 0, len(items))
	for _, it := range items {
		stages = append(stages, it.stage)
	}
	return ProductReport{ProductID: productID, Stages: stages}, nil
}

// Action reconciles one action, infrastructure errors returned as is.
func (a *Auditor) Action(ctx context.Context, actionID uuid.UUID) (Stage, error) {
	db, err := a.store.Action(ctx, actionID)
	if err != nil {
		return Stage{}, fmt.Errorf("audit.Auditor.Action: %w", err)
	}
	applied, err := a.ledger.Applied(ctx, []uuid.UUID{actionID})
	if err != nil {
		return Stage{}, fmt.Errorf("audit.Auditor.Action: %w", err)
	}
	rejected, err := a.ledger.Rejected(ctx, []uuid.UUID{actionID})
	if err != nil {
		return Stage{}, fmt.Errorf("audit.Auditor.Action: %w", err)
	}
	var ca *ChainAction
	if v, ok := applied[actionID]; ok {
		ca = &v
	}
	var cr *ChainRejection
	if v, ok := rejected[actionID]; ok {
		cr = &v
	}
	if db == nil && ca == nil && cr == nil {
		return Stage{}, fmt.Errorf("audit.Auditor.Action: action %s not found in DB and on chain", actionID)
	}
	return a.stage(ctx, db, ca, cr)
}

// All reconciles every submitted DB action together with every chain action
// and returns the flattened findings.
func (a *Auditor) All(ctx context.Context) ([]Finding, error) {
	dbActions, err := a.store.AllActions(ctx)
	if err != nil {
		return nil, fmt.Errorf("audit.Auditor.All: %w", err)
	}
	applied, err := a.ledger.Applied(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("audit.Auditor.All: %w", err)
	}
	ids, dbBy := union(nil, dbActions)
	dbIDs := make([]uuid.UUID, 0, len(dbActions))
	for _, id := range ids {
		if dbBy[id] != nil {
			dbIDs = append(dbIDs, id)
		}
	}
	var chainOnly []uuid.UUID
	for id := range applied {
		if _, ok := dbBy[id]; !ok {
			chainOnly = append(chainOnly, id)
		}
	}
	sort.Slice(chainOnly, func(i, j int) bool {
		bi, bj := applied[chainOnly[i]].BlockNumber, applied[chainOnly[j]].BlockNumber
		if bi != bj {
			return bi < bj
		}
		return bytes.Compare(chainOnly[i][:], chainOnly[j][:]) < 0
	})
	ids = append(ids, chainOnly...)

	rejected, err := a.ledger.Rejected(ctx, dbIDs)
	if err != nil {
		return nil, fmt.Errorf("audit.Auditor.All: %w", err)
	}

	var out []Finding
	for _, id := range ids {
		db := dbBy[id]
		var ca *ChainAction
		if v, ok := applied[id]; ok {
			ca = &v
		}
		var cr *ChainRejection
		if v, ok := rejected[id]; ok {
			cr = &v
		}
		s, err := a.stage(ctx, db, ca, cr)
		if err != nil {
			return nil, fmt.Errorf("audit.Auditor.All: %w", err)
		}
		out = append(out, s.Findings...)
	}
	return out, nil
}

// union appends the DB action ids missing from first and maps every id to its DB action.
func union(first []uuid.UUID, dbActions []Action) ([]uuid.UUID, map[uuid.UUID]*Action) {
	seen := make(map[uuid.UUID]bool, len(first)+len(dbActions))
	ids := make([]uuid.UUID, 0, len(first)+len(dbActions))
	for _, id := range first {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	dbBy := make(map[uuid.UUID]*Action, len(dbActions))
	for i := range dbActions {
		id := dbActions[i].ActionID
		dbBy[id] = &dbActions[i]
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, dbBy
}
