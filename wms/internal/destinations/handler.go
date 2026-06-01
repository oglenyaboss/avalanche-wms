package destinations

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"wms/internal/domain"
	"wms/internal/platform/httputil"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes регистрирует обработчик и на "", и на "/", чтобы под
// PathPrefix("/destinations").Subrouter() работали оба: /destinations и
// /destinations/ (gorilla/mux не делает redirect между ними).
func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("", h.GetDestinations).Methods(http.MethodGet)
	router.HandleFunc("/", h.GetDestinations).Methods(http.MethodGet)
}

// destinationItem — обрезанный DTO для wire-формата (без created_at/updated_at).
type destinationItem struct {
	DestinationID string  `json:"destination_id"`
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	Address       *string `json:"address"`
	WarehouseID   int64   `json:"warehouse_id"`
}

// destinationListResponse — структура поля data ответа.
type destinationListResponse struct {
	Destinations []destinationItem `json:"destinations"`
}

// GetDestinations — список магазинов-получателей. RBAC: только OPERATOR.
// Опциональный фильтр warehouse_id (int64). Пустой результат отдаёт
// {"destinations": []} (никогда не null).
func (h *Handler) GetDestinations(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireOperator(w, r); !ok {
		return
	}

	var warehouseID int64
	if raw := r.URL.Query().Get("warehouse_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "warehouse_id должен быть числом")
			return
		}
		// warehouse_id is a bigserial (>= 1); 0 or negative can never match a real
		// warehouse, so reject it rather than silently returning all destinations.
		if parsed <= 0 {
			httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "warehouse_id должен быть положительным числом")
			return
		}
		warehouseID = parsed
	}

	dests, err := h.svc.GetDestinations(r.Context(), warehouseID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		return
	}

	items := make([]destinationItem, 0, len(dests))
	for i := range dests {
		items = append(items, toItem(&dests[i]))
	}

	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{
		Success: true,
		Data:    destinationListResponse{Destinations: items},
		Error:   nil,
	})
}

func toItem(d *domain.Destination) destinationItem {
	return destinationItem{
		DestinationID: d.DestinationID.String(),
		Code:          d.Code,
		Name:          d.Name,
		Address:       d.Address,
		WarehouseID:   d.WarehouseID,
	}
}
