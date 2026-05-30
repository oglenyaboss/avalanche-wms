package assembly

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
	"wms/internal/ledger"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/allocate", h.Allocate).Methods(http.MethodPost)
	router.HandleFunc("/tasks", h.GetTasks).Methods(http.MethodGet)
	router.HandleFunc("/pick", h.Pick).Methods(http.MethodPost)
	router.HandleFunc("/scan-shipping-buffer", h.ScanShippingBuffer).Methods(http.MethodPost)
}

// Allocate - ищет новые заказы (NEW) для магазина (destinationID)
// и выводит количество собранных заказов, количество готовых товаров и проблемные товары (номер заказа и характеристика товара, включая количество)
func (h *Handler) Allocate(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOperator(w, r); !ok {
		return
	}

	var req AllocateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	destinationID, err := uuid.Parse(req.DestinationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "destination_id не uuid")
		return
	}

	result, err := h.svc.Allocate(r.Context(), destinationID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// GetTasks - возвращает задачи для оператора (какие товары нужно взять)
func (h *Handler) GetTasks(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	_ = operatorID

	destinationIDStr := r.URL.Query().Get("destination_id")
	if destinationIDStr == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "destination_id обязателен")
		return
	}

	destinationID, err := uuid.Parse(destinationIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "destination_id не uuid")
		return
	}

	operatorIDStr := r.URL.Query().Get("operator_id")
	var operatorIDFilter uuid.UUID
	if operatorIDStr != "" {
		operatorIDFilter, err = uuid.Parse(operatorIDStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "operator_id не uuid")
			return
		}
	}

	taskStatus := r.URL.Query().Get("status")
	if taskStatus == "" {
		taskStatus = string(domain.TaskStatusPending)
	}
	switch domain.TaskStatus(taskStatus) {
	case domain.TaskStatusPending, domain.TaskStatusInProgress, domain.TaskStatusDone, domain.TaskStatusCancelled:
	default:
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "неизвестный статус задачи")
		return
	}

	result, err := h.svc.GetTasks(r.Context(), destinationID, operatorIDFilter, taskStatus)
	if err != nil {
		httpStatus, code, message := mapServiceError(err)
		writeError(w, httpStatus, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// Pick - действие оператора (сканирует товар)
// После этого меняется статус задачи - Done, статус товара - Assembled
// создается outbox-event, товар добавляется в корзину оператора
func (h *Handler) Pick(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req PickRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	productID, err := uuid.Parse(req.ProductID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "product_id не uuid")
		return
	}

	result, err := h.svc.Pick(r.Context(), operatorID, productID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

func (h *Handler) ScanShippingBuffer(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}

	var req ScanShippingBufferRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	bufferBinID, err := uuid.Parse(req.BufferBinID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "buffer_bin_id не uuid")
		return
	}

	result, err := h.svc.ScanShippingBuffer(r.Context(), operatorID, bufferBinID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
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
		log.Printf("assembly.writeJSON encode: %v", err)
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
	case errors.Is(err, ErrDestinationNotFound):
		return http.StatusNotFound, "DESTINATION_NOT_FOUND", "магазин не найден"
	case errors.Is(err, ErrOrderNotNew):
		return http.StatusConflict, "ORDER_NOT_NEW", "Заказ не в статусе NEW"
	case errors.Is(err, ErrSKUNotFound):
		return http.StatusNotFound, "SKU_NOT_FOUND", "SKU не найден"
	case errors.Is(err, ErrInsufficientStock):
		return http.StatusUnprocessableEntity, "INSUFFICIENT_STOCK", "Недостаточно товара на складе"
	case errors.Is(err, ErrNoTaskForProduct):
		return http.StatusConflict, "NO_TASK_FOR_PRODUCT", "Нет задачи сборки для этого товара"
	case errors.Is(err, ErrProductNotAllocated):
		return http.StatusConflict, "PRODUCT_NOT_ALLOCATED", "Товар не в статусе ALLOCATED"
	case errors.Is(err, ledger.ErrChainEventRejected):
		return http.StatusConflict, "CHAIN_EVENT_REJECTED", "Подбор отклонён: размещение товара не подтверждено в блокчейне (FAILED)"
	case errors.Is(err, ErrInvalidInput):
		return http.StatusBadRequest, "INVALID_REQUEST", "Невалидные входные данные"
	case errors.Is(err, ErrBinNotShippingBuffer):
		return http.StatusBadRequest, "BIN_NOT_SHIPPING_BUFFER", "Ячейка не является буфером отгрузки"
	case errors.Is(err, ErrCartEmpty):
		return http.StatusConflict, "CART_EMPTY", "Корзина оператора пуста"
	case errors.Is(err, ErrDestinationMismatch):
		return http.StatusConflict, "DESTINATION_MISMATCH", "Товары в корзине принадлежат другому магазину"
	case errors.Is(err, ErrPartialPlacement):
		return http.StatusConflict, "PARTIAL_PLACEMENT", "Не все товары из корзины были размещены"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера"
	}
}
