package analytics

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"wms/internal/platform/httputil"
)

const (
	// recentFeedLimit bounds the recent failed/committed event lists.
	recentFeedLimit = 10
	// throughput window bounds: default two weeks, hard-capped at a quarter to
	// keep the query and payload bounded.
	defaultThroughputDays = 14
	maxThroughputDays     = 90
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes wires the read-only analytics endpoints. Registered on both ""
// and "/" so /analytics/summary and /analytics/summary/ both resolve under the
// PathPrefix subrouter (gorilla/mux does not redirect between them).
func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/summary", h.GetSummary).Methods(http.MethodGet)
	router.HandleFunc("/summary/", h.GetSummary).Methods(http.MethodGet)
	router.HandleFunc("/onchain", h.GetOnchain).Methods(http.MethodGet)
	router.HandleFunc("/onchain/", h.GetOnchain).Methods(http.MethodGet)
	router.HandleFunc("/throughput", h.GetThroughput).Methods(http.MethodGet)
	router.HandleFunc("/throughput/", h.GetThroughput).Methods(http.MethodGet)
}

// GetSummary — headline counters and lifecycle breakdowns. RBAC: ADMIN or
// OPERATOR (analytics is a managerial overview).
func (h *Handler) GetSummary(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireAdminOrOperator(w, r); !ok {
		return
	}
	rep, err := h.svc.GetSummary(r.Context())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: rep, Error: nil})
}

// GetOnchain — the blockchain confirmation hero.
func (h *Handler) GetOnchain(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireAdminOrOperator(w, r); !ok {
		return
	}
	rep, err := h.svc.GetOnchain(r.Context(), recentFeedLimit, recentFeedLimit)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: rep, Error: nil})
}

// GetThroughput — daily event volume per stage. Optional ?days=N (1..90,
// default 14).
func (h *Handler) GetThroughput(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireAdminOrOperator(w, r); !ok {
		return
	}

	days := defaultThroughputDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "days должен быть числом")
			return
		}
		if parsed < 1 || parsed > maxThroughputDays {
			httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "days должен быть в диапазоне 1..90")
			return
		}
		days = parsed
	}

	rep, err := h.svc.GetThroughput(r.Context(), days)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: rep, Error: nil})
}
