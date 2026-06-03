package products

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"wms/internal/platform/httputil"
)

const (
	defaultRecentLimit = 20
	maxRecentLimit     = 50
)

// service is the slice of *Service the handler needs (test seam).
type service interface {
	Recent(ctx context.Context, limit int) ([]RecentProduct, error)
	Timeline(ctx context.Context, key string) (Timeline, error)
}

type Handler struct {
	svc service
}

func NewHandler(svc service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/recent", h.GetRecent).Methods(http.MethodGet)
	router.HandleFunc("/recent/", h.GetRecent).Methods(http.MethodGet)
	router.HandleFunc("/timeline", h.GetTimeline).Methods(http.MethodGet)
	router.HandleFunc("/timeline/", h.GetTimeline).Methods(http.MethodGet)
}

func (h *Handler) GetRecent(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireAdminOrOperator(w, r); !ok {
		return
	}
	limit := defaultRecentLimit
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxRecentLimit {
		limit = maxRecentLimit
	}
	items, err := h.svc.Recent(r.Context(), limit)
	if err != nil {
		log.Printf("products: GetRecent -> 500: %v", err)
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: items, Error: nil})
}

func (h *Handler) GetTimeline(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireAdminOrOperator(w, r); !ok {
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		httputil.WriteError(w, http.StatusBadRequest, "MISSING_KEY", "Укажите product_id или QR-код")
		return
	}
	tl, err := h.svc.Timeline(r.Context(), key)
	if err != nil {
		if errors.Is(err, ErrProductNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "PRODUCT_NOT_FOUND", "Товар не найден")
			return
		}
		log.Printf("products: GetTimeline %q -> 500: %v", key, err)
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: tl, Error: nil})
}
