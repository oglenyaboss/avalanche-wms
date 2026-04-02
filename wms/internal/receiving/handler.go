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

type scanGateCargoplaceRequest struct {
	ShipmentID     string `json:"shipment_id"`
	CargoplaceCode string `json:"cargoplace_code"`
}

type acceptShipmentRequest struct {
	ShipmentID string `json:"shipment_id"`
}

type scanTableCargoplaceRequest struct {
	CargoplaceID string `json:"cargoplace_id"`
}

type scanBoxRequest struct {
	CargoplaceID string `json:"cargoplace_id"`
	BoxBarcode   string `json:"box_barcode"`
}

type scanSKURequest struct {
	CargoplaceID string `json:"cargoplace_id"`
	BoxID        string `json:"box_id"`
	Barcode      string `json:"barcode"`
}

type scanQRRequest struct {
	CargoplaceID string `json:"cargoplace_id"`
	BoxID        string `json:"box_id"`
	SKUID        string `json:"sku_id"`
	QRCode       string `json:"qr_code"`
}

type closeBoxRequest struct {
	BoxID string `json:"box_id"`
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
	router.HandleFunc("/table/scan-cargoplace", h.ScanTableCargoplace).Methods(http.MethodPost)
	router.HandleFunc("/table/scan-box", h.ScanBox).Methods(http.MethodPost)
	router.HandleFunc("/table/scan-sku", h.ScanSKU).Methods(http.MethodPost)
	router.HandleFunc("/table/scan-qr", h.ScanQR).Methods(http.MethodPost)
	router.HandleFunc("/table/close-box", h.CloseBox).Methods(http.MethodPost)
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

// ScanCargoplace handles the HTTP request for scanning a cargoplace within a shipment.
func (h *Handler) ScanCargoplace(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req scanGateCargoplaceRequest
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

// ScanTableCargoplace processes the scanning of a cargoplace at the sorting table,
// updating its status and returning details for further processing.
func (h *Handler) ScanTableCargoplace(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req scanTableCargoplaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	cargoplaceID, err := uuid.Parse(req.CargoplaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "cargoplace_id должен быть UUID")
		return
	}

	result, err := h.svc.ScanTableCargoplace(r.Context(), operatorID, cargoplaceID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

func (h *Handler) ScanBox(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req scanBoxRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	cargoplaceID, err := uuid.Parse(req.CargoplaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "cargoplace_id должен быть UUID")
		return
	}

	result, err := h.svc.ScanBox(r.Context(), operatorID, cargoplaceID, req.BoxBarcode)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

func (h *Handler) ScanSKU(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req scanSKURequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	cargoplaceID, err := uuid.Parse(req.CargoplaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "cargoplace_id должен быть UUID")
		return
	}
	boxID, err := uuid.Parse(req.BoxID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "box_id должен быть UUID")
		return
	}

	result, err := h.svc.ScanSKU(r.Context(), operatorID, cargoplaceID, boxID, req.Barcode)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

func (h *Handler) ScanQR(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req scanQRRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	cargoplaceID, err := uuid.Parse(req.CargoplaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "cargoplace_id должен быть UUID")
		return
	}
	boxID, err := uuid.Parse(req.BoxID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "box_id должен быть UUID")
		return
	}
	skuID, err := uuid.Parse(req.SKUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "sku_id должен быть UUID")
		return
	}

	result, err := h.svc.ScanQR(r.Context(), operatorID, cargoplaceID, boxID, skuID, req.QRCode)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

func (h *Handler) CloseBox(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req closeBoxRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	boxID, err := uuid.Parse(req.BoxID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "box_id должен быть UUID")
		return
	}

	result, err := h.svc.CloseBox(r.Context(), operatorID, boxID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// mapServiceError maps internal service errors to appropriate HTTP status codes and API error responses.
func mapServiceError(err error) (status int, code, message string) {
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
	case errors.Is(err, ErrCargoplaceNotFound):
		return http.StatusNotFound, "CARGOPLACE_NOT_FOUND", "Грузоместо не найдено"
	case errors.Is(err, ErrCargoplaceNotReceivedAtGate):
		return http.StatusConflict, "CARGOPLACE_NOT_RECEIVED_AT_GATE", "Грузоместо не в статусе RECEIVED_AT_GATE"
	case errors.Is(err, ErrCargoplaceNotInProgress):
		return http.StatusConflict, "CARGOPLACE_NOT_IN_PROGRESS", "Грузоместо не в статусе TABLE_IN_PROGRESS"
	case errors.Is(err, ErrBoxNotFound):
		return http.StatusNotFound, "BOX_NOT_FOUND", "Коробка не найдена"
	case errors.Is(err, ErrBoxNotOpen):
		return http.StatusConflict, "BOX_NOT_OPEN", "Коробка не открыта"
	case errors.Is(err, ErrBoxNotInCargoplace):
		return http.StatusBadRequest, "BOX_NOT_IN_CARGOPLACE", "Коробка не принадлежит грузоместу"
	case errors.Is(err, ErrBarcodeNotFound):
		return http.StatusNotFound, "BARCODE_NOT_FOUND", "Штрихкод не найден"
	case errors.Is(err, ErrSKUNotFound):
		return http.StatusNotFound, "SKU_NOT_FOUND", "SKU не найден"
	case errors.Is(err, ErrQRAlreadyExists):
		return http.StatusConflict, "QR_ALREADY_EXISTS", "QR-код уже зарегистрирован"
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
		writeError(w, http.StatusForbidden, "FORBIDDEN", "Только оператор может выполнять это действие")
		return uuid.Nil, false
	}

	return operatorID, true
}
