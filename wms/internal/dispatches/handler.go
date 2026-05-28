package dispatches

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"wms/internal/auth"
	"wms/internal/domain"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(mx *mux.Router) {
	mx.HandleFunc("/", h.GetDispatches).Methods("GET")
	mx.HandleFunc("/", h.NewDispatch).Methods("POST")
	mx.HandleFunc("/{dispatch_id}", h.GetDispatchByID).Methods("GET")
}

type DispatchFilter struct {
	Status        *domain.OutboundDispatchStatus
	DestinationID uuid.UUID
	WarehouseID   int
}

func (h *Handler) GetDispatches(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOperator(w, r); !ok {
		return
	}

	vars := r.URL.Query()
	filter := DispatchFilter{}

	if status := vars.Get("status"); status != "" {
		filter.Status = (*domain.OutboundDispatchStatus)(&status)
	} else {
		filter.Status = nil
	}
	if destID := vars.Get("destination_id"); destID != "" {
		x, err := uuid.Parse(destID)
		if err != nil {
			respondWithError(w, "Invalid request: "+err.Error(), "INVALID_REQUEST", 400)
			return
		}
		filter.DestinationID = x
	} else {
		filter.DestinationID = uuid.Nil
	}
	if whId := vars.Get("warehouse_id"); whId != "" {
		x, err := strconv.Atoi(whId)
		if err != nil {
			respondWithError(w, "Invalid request: "+err.Error(), "INVALID_REQUEST", 400)
			return
		}
		filter.WarehouseID = x
	} else {
		filter.WarehouseID = -1
	}

	dispatches, err := h.svc.GetDispatches(r.Context(), filter)
	if err != nil {
		respondWithError(w, err.Error(), "INTERNAL_ERROR", 503)
		return
	}

	respondWithSuccess(w, dispatches)
}

type NewDispatchQuery struct {
	DestinationID uuid.UUID `json:"destination_id"`
	VehicleNumber string    `json:"vehicle_number"`
	DriverName    string    `json:"driver_name"`
	DriverPhone   *string   `json:"driver_phone,omitempty"`
	ScheduledAt   time.Time `json:"scheduled_at"`
}

func (h *Handler) NewDispatch(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOperator(w, r); !ok {
		return
	}

	var inputData NewDispatchQuery
	err := decodeJSON(r, &inputData)
	if err != nil {
		respondWithError(w, "Invalid request body: "+err.Error(), "INVALID_REQUEST", 400)
		return
	}
	if inputData.DriverName == "" {
		respondWithError(w, "Invalid request body: you need to pass a driver_name", "INVALID_REQUEST", 400)
		return
	}
	if inputData.VehicleNumber == "" {
		respondWithError(w, "Invalid request body: you need to pass a vehicle_number", "INVALID_REQUEST", 400)
		return
	}
	if inputData.ScheduledAt.Before(time.Now()) {
		respondWithError(w, "Invalid request body: scheduled_at should not be past time", "INVALID_REQUEST", 400)
		return
	}

	dispatch, err := h.svc.CreateNewDispatch(r.Context(), &inputData)
	if err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			respondWithError(w, "DESTINATION_NOT_FOUND", "NOT_FOUND", 404)
			return
		}
		respondWithError(w, fmt.Errorf("failed to create new dispatch: %w", err).Error(), "INTERNAL_ERROR", 503)
		return
	}

	respondWithSuccess(w, map[string]interface{}{
		"dispatch": dispatch,
	})
}

func (h *Handler) GetDispatchByID(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOperator(w, r); !ok {
		return
	}

	vars := mux.Vars(r)
	dispatchID, err := uuid.Parse(vars["dispatch_id"])
	if err != nil {
		respondWithError(w, fmt.Errorf("invalid dispatch_id format: %w", err).Error(), "INVALID_REQUEST", 400)
		return
	}

	dispatch, err := h.svc.GetDispatchByID(r.Context(), dispatchID)
	if err != nil {
		if errors.Is(err, ErrDispatchNotFound) {
			respondWithError(w, "DISPATCH_NOT_FOUND", "NOT_FOUND", 404)
			return
		}
		respondWithError(w, fmt.Errorf("failed to get dispatch by id %s: %w", dispatchID, err).Error(), "INTERNAL_ERROR", 503)
		return
	}

	respondWithSuccess(w, map[string]interface{}{
		"dispatch": dispatch,
	})
}

func requireOperator(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	operatorID := auth.UserIDFromCtx(r.Context())
	if operatorID == uuid.Nil {
		respondWithError(w, "Требуется авторизация", "UNAUTHORIZED", 401)
		return uuid.Nil, false
	}
	role := auth.UserRoleFromCtx(r.Context())
	if role != domain.UserRoleOperator {
		respondWithError(w, "Только оператор может выполнять это действие", "FORBIDDEN", 403)
		return uuid.Nil, false
	}
	return operatorID, true
}

func decodeJSON(r *http.Request, dest any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func respondWithSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	response := map[string]interface{}{
		"success": true,
		"data":    data,
		"error":   nil,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("failed to encode success response: %v\n", err)
	}
}

func respondWithError(w http.ResponseWriter, errMsg, status string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	er := ApiError{Message: errMsg, Code: status}
	response := map[string]interface{}{
		"success": false,
		"data":    nil,
		"error":   er,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("failed to encode error response: %v\n", err)
	}
}
