package audit

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/platform/httputil"
	"wms/internal/signedaction"
)

// DetailsErrorRowMissing is the detailsError code sent when the domain row
// behind an action is absent.
const DetailsErrorRowMissing = "DOMAIN_ROW_MISSING"

// DetailsErrorInconsistent is the detailsError code sent when the domain rows
// behind an action disagree on a single detail value.
const DetailsErrorInconsistent = "DOMAIN_INCONSISTENT"

// productActions is the slice of *Repository the handler needs (test seam).
type productActions interface {
	ProductID(ctx context.Context, key string) (uuid.UUID, error)
	ActionsForProduct(ctx context.Context, productID uuid.UUID) ([]Action, error)
	Details(ctx context.Context, a Action) (Details, error)
}

// Handler serves the read-only audit endpoint: for one product, every submitted
// signed action with its salt and the details recomputed from the domain tables.
// It returns only database data and the salt — nothing from the chain (issue #18).
type Handler struct {
	repo productActions
}

func NewHandler(repo productActions) *Handler { return &Handler{repo: repo} }

func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/products/{key}", h.GetProduct).Methods(http.MethodGet)
}

// GetProduct returns the audit record of one product identified by uuid or QR code.
func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireAuditorOrAdmin(w, r); !ok {
		return
	}
	key := mux.Vars(r)["key"]
	productID, err := h.repo.ProductID(r.Context(), key)
	if err != nil {
		if errors.Is(err, ErrProductNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "PRODUCT_NOT_FOUND", "Товар не найден")
			return
		}
		log.Printf("audit: GetProduct %q -> 500: %v", key, err)
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		return
	}
	actions, err := h.repo.ActionsForProduct(r.Context(), productID)
	if err != nil {
		log.Printf("audit: GetProduct %q -> 500: %v", key, err)
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		return
	}
	view := productAuditView{
		ProductID: productID.String(),
		ItemID:    signedaction.Hex32(signedaction.ItemID(productID)),
		Actions:   make([]actionView, 0, len(actions)),
	}
	for _, a := range actions {
		av, err := h.actionView(r.Context(), a)
		if err != nil {
			log.Printf("audit: GetProduct %q -> 500: %v", key, err)
			httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
			return
		}
		view.Actions = append(view.Actions, av)
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: view})
}

// actionView renders one action; a broken domain record is surfaced to the
// auditor as detailsError instead of failing the whole response.
func (h *Handler) actionView(ctx context.Context, a Action) (actionView, error) {
	v := actionView{
		ActionID:     a.ActionID.String(),
		OperationID:  a.OperationID.String(),
		PartIndex:    a.PartIndex,
		ActionType:   a.ActionType,
		ItemIDs:      uuidStrings(a.ItemIDs),
		Salt:         signedaction.Hex32(a.Salt),
		SubmittedAt:  a.SubmittedAt.UTC().Format(time.RFC3339Nano),
		OperatorID:   a.OperatorID.String(),
		Operator:     a.Operator,
		OrgID:        a.OrgID.String(),
		OrgName:      a.OrgName,
		KeyID:        signedaction.Hex32(a.KeyID),
		ChainStatus:  a.ChainStatus,
		RejectReason: a.RejectReason,
	}
	d, err := h.repo.Details(ctx, a)
	switch {
	case err == nil:
		v.Details = newDetailsView(d)
	case errors.Is(err, ErrDomainRowMissing):
		log.Printf("audit: action %s details: %v", a.ActionID, err)
		msg := DetailsErrorRowMissing
		v.DetailsError = &msg
	case errors.Is(err, ErrDomainInconsistent):
		log.Printf("audit: action %s details: %v", a.ActionID, err)
		msg := DetailsErrorInconsistent
		v.DetailsError = &msg
	default:
		return actionView{}, err
	}
	return v, nil
}
