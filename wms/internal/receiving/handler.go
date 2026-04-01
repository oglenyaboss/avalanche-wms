package receiving

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
)

type Handler struct {
	svc *Service
}

type scanTTNRequest struct {
	TTNCode string `json:"ttn_code"`
}

type scanCargoplaceRequest struct {
	ShipmentID     string `json:"shipment_id"`
	CargoplaceCode string `json:"cargoplace_code"`
}

type acceptShipmentRequest struct {
	ShipmentID string `json:"shipment_id"`
}

type envelope struct {
	Success bool       `json:"success"`
	Data    any        `json:"data"`
	Error   *apiErrObj `json:"error"`
}

type apiErrObj struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/gate/scan-ttn", h.ScanTTN).Methods(http.MethodPost)
	router.HandleFunc("/gate/scan-cargoplace", h.ScanCargoplace).Methods(http.MethodPost)
	router.HandleFunc("/gate/accept-shipment", h.AcceptShipment).Methods(http.MethodPost)
}

// ScanTTN processes the scanning of a TTN code, updating shipment status and returning shipment details.
func (h *Handler) ScanTTN(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req scanTTNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	result, err := h.svc.ScanTTN(r.Context(), operatorID, req.TTNCode)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// ScanCargoplace handles the HTTP request for scanning a cargoplace within a shipment
func (h *Handler) ScanCargoplace(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req scanCargoplaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	shipmentID, err := uuid.Parse(req.ShipmentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "shipment_id должен быть UUID")
		return
	}

	result, err := h.svc.ScanCargoplace(r.Context(), operatorID, shipmentID, req.CargoplaceCode)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// AcceptShipment handles the HTTP request for accepting a shipment at the receiving gate, finalizing the receiving process.
func (h *Handler) AcceptShipment(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req acceptShipmentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	shipmentID, err := uuid.Parse(req.ShipmentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "shipment_id должен быть UUID")
		return
	}

	result, err := h.svc.AcceptShipment(r.Context(), operatorID, shipmentID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// mapServiceError maps internal service errors to appropriate HTTP status codes and API error responses.
func mapServiceError(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrTTNNotFound):
		return http.StatusNotFound, "TTN_NOT_FOUND", "TTN не найдена"
	case errors.Is(err, ErrShipmentAlreadyClosed):
		return http.StatusConflict, "SHIPMENT_ALREADY_CLOSED", "Поставка уже закрыта"
	case errors.Is(err, ErrShipmentNotInProgress):
		return http.StatusConflict, "SHIPMENT_NOT_IN_PROGRESS", "Поставка не в статусе GATE_IN_PROGRESS"
	case errors.Is(err, ErrCargoplaceNotInShipment):
		return http.StatusBadRequest, "CARGOPLACE_NOT_IN_SHIPMENT", "Грузоместо не принадлежит данной поставке"
	case errors.Is(err, ErrCargoplaceAlreadyReceive):
		return http.StatusConflict, "CARGOPLACE_ALREADY_RECEIVED", "Грузоместо уже отсканировано"
	case errors.Is(err, ErrInvalidInput):
		return http.StatusBadRequest, "INVALID_REQUEST", "Невалидные входные данные"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера"
	}
}

// decodeJSON decodes JSON from the request body into the provided destination struct.
func decodeJSON(r *http.Request, dest any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func writeSuccess(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, envelope{
		Success: true,
		Data:    data,
		Error:   nil,
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, envelope{
		Success: false,
		Data:    nil,
		Error: &apiErrObj{
			Code:    code,
			Message: message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("receiving.writeJSON encode: %v", err)
	}
}

func requireOperator(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	operatorID := auth.UserIDFromCtx(r.Context())
	if operatorID == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется авторизация")
		return uuid.Nil, false
	}

	role := auth.UserRoleFromCtx(r.Context())
	if role != domain.UserRoleOperator {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "Только оператор КПП может выполнять это действие")
		return uuid.Nil, false
	}

	return operatorID, true
}
