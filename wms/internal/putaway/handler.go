package putaway

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

type ScanBufferRequest struct {
	BufferBinId string `json:"buffer_bin_id"`
}

type ScanBufferResponse struct {
	BufferBinId   string                      `json:"buffer_bin_id"`
	BufferCode    string                      `json:"buffer_code"`
	Products      []ProductBufferItemResponse `json:"products"`
	TotalProducts int                         `json:"total_products"`
}

type ProductBufferItemResponse struct {
	ProductID string `json:"product_id"`
	SKUName   string `json:"sku_name"`
	QRCode    string `json:"qr_code"`
	Status    string `json:"status"`
}

type ScanProductRequest struct {
	ProductID   string `json:"product_id"`
	BufferBinID string `json:"buffer_bin_id"`
}

type ScanProductResponse struct {
	ProductID string `json:"product_id"`
	SKUName   string `json:"sku_name"`
	QRCode    string `json:"qr_code"`
	CartSize  int    `json:"cart_size"`
}

type ScanStorageBinRequest struct {
	ProductIDs   []string `json:"product_ids"`
	StorageBinID string   `json:"storage_bin_id"`
}

type ScanStorageBinResponse struct {
	StorageBinID        string `json:"storage_bin_id"`
	StorageBinCode      string `json:"storage_bin_code"`
	ProductsPlaced      int    `json:"products_placed"`
	OutboxEventsCreated int    `json:"outbox_events_created"`
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
	router.HandleFunc("/scan-buffer", h.ScanBuffer).Methods(http.MethodPost)
	router.HandleFunc("/scan-product", h.ScanProduct).Methods(http.MethodPost)
	router.HandleFunc("/scan-storage-bin", h.ScanStorageBin).Methods(http.MethodPost)
}

// Сканирование буфферной ячейки - выдает все товары, которые хранятся в данном буфере
func (h *Handler) ScanBuffer(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req ScanBufferRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	bufferBinID, err := uuid.Parse(req.BufferBinId)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "buffer_bin_id должен быть UUID")
		return
	}

	result, err := h.svc.GetBufferProducts(r.Context(), operatorID, bufferBinID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

// сканирование товара для добавления - берем товары из буфера для последующего добавления
func (h *Handler) ScanProduct(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req ScanProductRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	productID, err := uuid.Parse(req.ProductID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "product_id должен быть UUID")
		return
	}

	bufferBinID, err := uuid.Parse(req.BufferBinID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "buffer_bin_id должен быть UUID")
		return
	}

	result, err := h.svc.AddToPutawayCart(r.Context(), operatorID, productID, bufferBinID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

// Сканирование ячейки хранения товара и размещения товара - сканируем мезонин и размещаем туда товар
func (h *Handler) ScanStorageBin(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req ScanStorageBinRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	if len(req.ProductIDs) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "product_ids не может быть пустым")
		return
	}

	productIDS := make([]uuid.UUID, 0, len(req.ProductIDs))
	for _, idStr := range req.ProductIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "product_id должен быть UUID")
			return
		}
		productIDS = append(productIDS, id)
	}

	storageBinID, err := uuid.Parse(req.StorageBinID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "storage_bin_id должен быть UUID")
		return
	}

	result, err := h.svc.PlaceProductsToStorageBin(r.Context(), operatorID, productIDS, storageBinID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

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
		log.Printf("putaway.writeJSON encode: %v", err)
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

func mapServiceError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, ErrBufferBinNotFound):
		return http.StatusNotFound, "BUFFER_BIN_NOT_FOUND", "Буферная ячейка не найдена"
	case errors.Is(err, ErrStorageBinNotFound):
		return http.StatusNotFound, "STORAGE_BIN_NOT_FOUND", "Ячейка хранения не найдена"
	case errors.Is(err, ErrProductNotFound):
		return http.StatusNotFound, "PRODUCT_NOT_FOUND", "Товар не найден"
	case errors.Is(err, ErrProductNotInBuffer):
		return http.StatusConflict, "PRODUCT_NOT_IN_BUFFER", "Товар не находится в указанной буферной ячейке"
	case errors.Is(err, ErrProductNotReceived):
		return http.StatusConflict, "PRODUCT_NOT_RECEIVED", "Товар не в статусе RECEIVED"
	case errors.Is(err, ErrInvalidInput):
		return http.StatusBadRequest, "INVALID_REQUEST", "Невалидные входные данные"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера"
	}
}
