package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
	"wms/internal/signedaction"
)

// fakeRepo stubs productActions the way products handler tests stub service.
type fakeRepo struct {
	productID     uuid.UUID
	productIDErr  error
	actions       []Action
	details       Details
	detailsErr    error
	domainItemsFn func(a Action) ([]uuid.UUID, error)
}

func (f *fakeRepo) ProductID(_ context.Context, _ string) (uuid.UUID, error) {
	return f.productID, f.productIDErr
}
func (f *fakeRepo) ActionsForProduct(_ context.Context, _ uuid.UUID) ([]Action, error) {
	return f.actions, nil
}
func (f *fakeRepo) Details(_ context.Context, _ Action) (Details, error) {
	return f.details, f.detailsErr
}

// DomainItems falls back to the action's item_ids copy when no override is set.
func (f *fakeRepo) DomainItems(_ context.Context, a Action) ([]uuid.UUID, error) {
	if f.domainItemsFn != nil {
		return f.domainItemsFn(a)
	}
	return a.ItemIDs, nil
}

// asRole injects an authenticated identity — RequireAuditorOrAdmin (top of the
// handler) is the real gate; the subrouter middleware only populates this
// context. Mirrors wms/internal/products/handler_test.go.
func asRole(role domain.UserRole) func(*http.Request) *http.Request {
	return func(req *http.Request) *http.Request {
		ctx := auth.ContextWithIdentity(req.Context(), uuid.New(), role)
		return req.WithContext(ctx)
	}
}

func serveAudit(repo productActions, role domain.UserRole, path string) *httptest.ResponseRecorder {
	r := mux.NewRouter()
	sub := r.PathPrefix("/audit").Subrouter()
	NewHandler(repo).RegisterRoutes(sub)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, asRole(role)(httptest.NewRequest(http.MethodGet, path, nil)))
	return rec
}

// putAwayAction is one submitted signed putaway part.
func putAwayAction(productID uuid.UUID) Action {
	operatorID := uuid.New()
	return Action{
		ActionID:    uuid.New(),
		OperationID: uuid.New(),
		PartIndex:   1,
		ActionType:  signedaction.TypePutAway,
		ItemIDs:     []uuid.UUID{productID},
		Salt:        [32]byte{1, 2, 3},
		SubmittedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
		OperatorID:  operatorID,
		Operator:    "operator",
		KeyOwnerID:  operatorID,
		OrgID:       uuid.New(),
		OrgName:     "ООО Ромашка",
		KeyID:       [32]byte{4, 5, 6},
	}
}

type auditEnvelope struct {
	Success bool       `json:"success"`
	Data    productDTO `json:"data"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error"`
}

type productDTO struct {
	ProductID string      `json:"productId"`
	ItemID    string      `json:"itemId"`
	Actions   []actionDTO `json:"actions"`
}

type actionDTO struct {
	ActionID     string   `json:"actionId"`
	ActionType   int      `json:"actionType"`
	ItemIDs      []string `json:"itemIds"`
	KeyOwnerID   string   `json:"keyOwnerId"`
	Salt         string   `json:"salt"`
	SubmittedAt  string   `json:"submittedAt"`
	ChainStatus  string   `json:"chainStatus"`
	RejectReason *int16   `json:"rejectReason"`
	Details      *struct {
		StorageBinID string `json:"storageBinId"`
		OperatorID   string `json:"operatorId"`
		OccurredAtMs uint64 `json:"occurredAtMs"`
	} `json:"details"`
	DetailsError *string `json:"detailsError"`
}

func TestGetProduct_AuditorSeesPutAwayDetails(t *testing.T) {
	productID := uuid.New()
	binID := uuid.New()
	action := putAwayAction(productID)
	repo := &fakeRepo{
		productID: productID,
		actions:   []Action{action},
		details: Details{
			ActionType:   signedaction.TypePutAway,
			StorageBinID: binID,
			OperatorID:   uuid.New(),
			OccurredAtMs: 1700000000000,
		},
	}
	rec := serveAudit(repo, domain.UserRoleAuditor, "/audit/products/"+productID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var env auditEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.Success || len(env.Data.Actions) != 1 {
		t.Fatalf("want success with one action, got %s", rec.Body.String())
	}
	if env.Data.ProductID != productID.String() {
		t.Fatalf("productId = %q, want %q", env.Data.ProductID, productID)
	}
	wantItem := signedaction.Hex32(signedaction.ItemID(productID))
	if env.Data.ItemID != wantItem {
		t.Fatalf("itemId = %q, want %q", env.Data.ItemID, wantItem)
	}
	a := env.Data.Actions[0]
	if a.ActionType != int(signedaction.TypePutAway) {
		t.Fatalf("actionType = %d, want %d", a.ActionType, signedaction.TypePutAway)
	}
	if len(a.ItemIDs) != 1 || a.ItemIDs[0] != productID.String() {
		t.Fatalf("itemIds = %v, want [%s]", a.ItemIDs, productID)
	}
	if want := signedaction.Hex32([32]byte{1, 2, 3}); a.Salt != want {
		t.Fatalf("salt = %q, want %q", a.Salt, want)
	}
	if a.KeyOwnerID != action.KeyOwnerID.String() {
		t.Fatalf("keyOwnerId = %q, want %q", a.KeyOwnerID, action.KeyOwnerID)
	}
	if a.SubmittedAt != "2025-01-02T03:04:05Z" {
		t.Fatalf("submittedAt = %q", a.SubmittedAt)
	}
	if a.Details == nil || a.Details.StorageBinID != binID.String() {
		t.Fatalf("details = %+v, want storageBinId %s", a.Details, binID)
	}
	if a.DetailsError != nil {
		t.Fatalf("detailsError = %q, want nil", *a.DetailsError)
	}
}

func TestGetProduct_OperatorForbidden(t *testing.T) {
	repo := &fakeRepo{productID: uuid.New()}
	rec := serveAudit(repo, domain.UserRoleOperator, "/audit/products/x")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGetProduct_UnknownKey404(t *testing.T) {
	repo := &fakeRepo{productIDErr: fmt.Errorf("audit.Repository.ProductID: %w", ErrProductNotFound)}
	rec := serveAudit(repo, domain.UserRoleAuditor, "/audit/products/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetProduct_InconsistentDomainRowsAreSurfaced(t *testing.T) {
	productID := uuid.New()
	detailsErr := fmt.Errorf("audit.Repository.Details products: %w", ErrDomainInconsistent)
	repo := &fakeRepo{
		productID:  productID,
		actions:    []Action{putAwayAction(productID)},
		detailsErr: detailsErr,
	}
	rec := serveAudit(repo, domain.UserRoleAuditor, "/audit/products/"+productID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var env auditEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	a := env.Data.Actions[0]
	if a.Details != nil {
		t.Fatalf("details = %+v, want null", a.Details)
	}
	if a.DetailsError == nil || *a.DetailsError != "DOMAIN_INCONSISTENT" {
		t.Fatalf("detailsError = %v, want %q", a.DetailsError, "DOMAIN_INCONSISTENT")
	}
}

func TestGetProduct_MissingDomainRowIsSurfaced(t *testing.T) {
	productID := uuid.New()
	detailsErr := fmt.Errorf("audit.Repository.Details putaways: %w", ErrDomainRowMissing)
	repo := &fakeRepo{
		productID:  productID,
		actions:    []Action{putAwayAction(productID)},
		detailsErr: detailsErr,
	}
	rec := serveAudit(repo, domain.UserRoleAuditor, "/audit/products/"+productID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var env auditEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	a := env.Data.Actions[0]
	if a.Details != nil {
		t.Fatalf("details = %+v, want null", a.Details)
	}
	if a.DetailsError == nil || *a.DetailsError != "DOMAIN_ROW_MISSING" {
		t.Fatalf("detailsError = %v, want %q", a.DetailsError, "DOMAIN_ROW_MISSING")
	}
}

func TestGetProduct_DetailsInternalError500(t *testing.T) {
	productID := uuid.New()
	repo := &fakeRepo{
		productID:  productID,
		actions:    []Action{putAwayAction(productID)},
		detailsErr: errors.New("connection reset"),
	}
	rec := serveAudit(repo, domain.UserRoleAuditor, "/audit/products/"+productID.String())
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}
