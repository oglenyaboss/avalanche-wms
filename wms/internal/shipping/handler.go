package shipping

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/platform/httputil"
)

// maxProductIDsPerRequest bounds the product_ids[] array a single ship request may carry,
// so one authenticated call cannot pin a long-lived transaction over an unbounded lock set.
const maxProductIDsPerRequest = 200

type Handler struct {
	svc *Service
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
	router.HandleFunc("/scan-driver", h.ScanDriver).Methods(http.MethodPost)
	// Отгрузка подписана (ADR-0001, #15): черновик и submit вместо беззнакового /ship.
	router.HandleFunc("/ship/draft", h.DraftShip).Methods(http.MethodPost)
	router.HandleFunc("/ship/submit", h.SubmitShip).Methods(http.MethodPost)
}

func (h *Handler) ScanBuffer(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	var req scanBufferRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	bufferBinID, err := uuid.Parse(req.BufferBinID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "buffer_bin_id должен быть UUID")
		return
	}

	result, err := h.svc.ScanBuffer(r.Context(), operatorID, bufferBinID)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

func (h *Handler) ScanDriver(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	var req scanDriverRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	result, err := h.svc.ScanDriver(r.Context(), operatorID, req.DispatchCode)
	if err != nil {
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

func mapServiceError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, ErrBinNotFound):
		return http.StatusNotFound, "BIN_NOT_FOUND", "Ячейка не найдена"
	case errors.Is(err, ErrBinNotShippingBuffer):
		return http.StatusBadRequest, "BIN_NOT_SHIPPING_BUFFER", "Ячейка не является буфером отгрузки"
	case errors.Is(err, ErrDispatchNotFound):
		return http.StatusNotFound, "DISPATCH_NOT_FOUND", "Рейс не найден"
	case errors.Is(err, ErrDispatchAlreadyDeparted):
		return http.StatusConflict, "DISPATCH_ALREADY_DEPARTED", "Рейс уже отправлен"
	case errors.Is(err, ErrDispatchCancelled):
		//nolint:misspell // Must match API contract and DB enum value: CANCELLED.
		return http.StatusConflict, "DISPATCH_CANCELLED", "Рейс отменён"
	case errors.Is(err, ErrDestinationMismatch):
		return http.StatusConflict, "DESTINATION_MISMATCH", "Рейс назначен на другую зону"
	case errors.Is(err, ErrDispatchNotAtGate):
		return http.StatusConflict, "DISPATCH_NOT_AT_GATE", "Рейс не находится у ворот"
	case errors.Is(err, ErrBufferEmpty):
		return http.StatusConflict, "BUFFER_EMPTY", "Буфер отгрузки пуст"
	case errors.Is(err, ErrProductNotInBuffer):
		return http.StatusConflict, "PRODUCT_NOT_IN_BUFFER", "Товар не найден в буфере или не готов к отгрузке"
	case errors.Is(err, ErrPickingNotConfirmed):
		// Конверт ошибок shipping не несёт details, поэтому количество неподтверждённых
		// товаров уходит в сообщение (issue #15).
		var perr *PickingNotConfirmedError
		count := 0
		if errors.As(err, &perr) {
			count = len(perr.ProductIDs)
		}
		return http.StatusConflict, "PICKING_NOT_CONFIRMED",
			fmt.Sprintf("Сборка части товаров (%d шт.) ещё не подтверждена в цепи. Повторите отгрузку чуть позже", count)
	case errors.Is(err, ErrDraftStale):
		return http.StatusConflict, "DRAFT_STALE", "Состав отгрузки изменился, подтвердите отгрузку заново"
	case errors.Is(err, ErrInvalidInput):
		return http.StatusBadRequest, "INVALID_REQUEST", "Невалидные входные данные"
	default:
		log.Printf("shipping: unmapped service error -> 500: %v", err)
		return http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера"
	}
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
		log.Printf("shipping.writeJSON encode: %v", err)
	}
}
