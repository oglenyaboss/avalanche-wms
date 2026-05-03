package dispatches

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"wms/internal/domain"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
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
	mx.HandleFunc("/{dispatch_id}", h.GetDispatchById).Methods("GET")
}

type DispatchFilter struct {
	Status        *domain.OutboundDispatchStatus
	DestinationId uuid.UUID
	WarehouseId   int
}

func (h *Handler) GetDispatches(w http.ResponseWriter, r *http.Request) {
	vars := r.URL.Query()
	filter := DispatchFilter{}

	if status := vars.Get("status"); status != "" {
		filter.Status = (*domain.OutboundDispatchStatus)(&status)
	} else {
		filter.Status = nil
	}
	if destId := vars.Get("destination_id"); destId != "" {
		x, err := uuid.Parse(destId)
		if err != nil {
			respondWithError(w, "Invalid request: "+err.Error(), 400)
			return
		}
		filter.DestinationId = x
	} else {
		filter.DestinationId = uuid.Nil
	}
	if whId := vars.Get("warehouse_id"); whId != "" {
		x, err := strconv.Atoi(whId)
		if err != nil {
			respondWithError(w, "Invalid request: "+err.Error(), 400)
			return
		}
		filter.WarehouseId = x
	} else {
		filter.WarehouseId = -1
	}

	dispatches, err := h.svc.GetDispatches(r.Context(), filter)
	if err != nil {
		respondWithError(w, err.Error(), 503)
		return
	}

	respondWithSuccess(w, dispatches)
}

type NewDispatchQuery struct {
	DestinationId uuid.UUID `json:"destination_id"`
	VehicleNumber string    `json:"vehicle_number"`
	DriverName    string    `json:"driver_name"`
	DriverPhone   *string   `json:"driver_phone,omitempty"`
	ScheduledAt   time.Time `json:"scheduled_at"`
}

func (h *Handler) NewDispatch(w http.ResponseWriter, r *http.Request) {
	var inputData NewDispatchQuery
	err := decodeJSON(r, &inputData)
	if err != nil {
		respondWithError(w, "Invalid request body: "+err.Error(), 400)
		return
	}

	if inputData.ScheduledAt.Before(time.Now()) {
		respondWithError(w, "Invalid request body: scheduled_at should not be past time", 400)
		return
	}

	dispatch, err := h.svc.CreateNewDispatch(r.Context(), &inputData)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondWithError(w, "DESTINATION_NOT_FOUND", 404)
			return
		}
		respondWithError(w, fmt.Errorf("failed to create new dispatch: %w", err).Error(), 503)
		return
	}

	respondWithSuccess(w, map[string]interface{}{
		"dispatch": dispatch,
	})
}

func (h *Handler) GetDispatchById(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	dispatchID, err := uuid.Parse(vars["dispatch_id"])
	if err != nil {
		respondWithError(w, fmt.Errorf("invalid dispatch_id format: %w", err).Error(), 400)
		return
	}

	dispatch, err := h.svc.GetDispatchById(r.Context(), dispatchID)
	if err != nil {
		respondWithError(w, fmt.Errorf("failed to get dispatch by id %s: %w", dispatchID, err).Error(), 503)
		return
	}

	if dispatch == (domain.OutboundDispatch{}) {
		respondWithError(w, "DISPATCH_NOT_FOUND", 404)
		return
	}

	respondWithSuccess(w, map[string]interface{}{
		"dispatch": dispatch,
	})
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

func respondWithError(w http.ResponseWriter, errMsg string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	response := map[string]interface{}{
		"success": false,
		"data":    nil,
		"error":   errMsg,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("failed to encode error response: %v\n", err)
	}
}
