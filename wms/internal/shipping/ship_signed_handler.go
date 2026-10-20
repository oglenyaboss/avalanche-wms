package shipping

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"wms/internal/passkeys"
	"wms/internal/platform/httputil"
	"wms/internal/signedops"
)

const (
	// maxShipDraftBodyBytes ограничивает тело черновика: spot-выбор ≤200 UUID,
	// bulk (пустой product_ids) товара в теле не несёт.
	maxShipDraftBodyBytes = 16 << 10
	// maxShipSubmitBodyBytes вмещает подтверждения всех частей крупной операции
	// (черновик режет буфер на части по 2000 товаров, ADR-0001 §10).
	maxShipSubmitBodyBytes = 1 << 20
)

type shipDraftHTTPRequest struct {
	BufferBinID string   `json:"buffer_bin_id"`
	DispatchID  string   `json:"dispatch_id"`
	ProductIDs  []string `json:"product_ids"`
}

// DraftShip возвращает многочастный подписанный черновик отгрузки буфера на рейс.
func (h *Handler) DraftShip(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxShipDraftBodyBytes)
	var req shipDraftHTTPRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	bufferBinID, err := uuid.Parse(req.BufferBinID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "buffer_bin_id должен быть UUID")
		return
	}
	dispatchID, err := uuid.Parse(req.DispatchID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "dispatch_id должен быть UUID")
		return
	}

	if len(req.ProductIDs) > maxProductIDsPerRequest {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("product_ids не может содержать более %d элементов", maxProductIDsPerRequest))
		return
	}

	productIDs := make([]uuid.UUID, 0, len(req.ProductIDs))
	for _, rawID := range req.ProductIDs {
		productID, err := uuid.Parse(rawID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "product_ids должен содержать только UUID")
			return
		}
		productIDs = append(productIDs, productID)
	}

	op, rpID, err := h.svc.DraftShip(r.Context(), operatorID, bufferBinID, dispatchID, productIDs)
	if err != nil {
		h.writeShipError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, signedops.NewDraftView(op, rpID))
}

// SubmitShip проверяет passkey-подписи всех частей и применяет операцию отгрузки.
func (h *Handler) SubmitShip(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxShipSubmitBodyBytes)
	var req signedops.SubmitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}
	operationID, assertions, err := req.Decode()
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	result, err := h.svc.SubmitShip(r.Context(), operatorID, operationID, assertions)
	if err != nil {
		h.writeShipError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// writeShipError маппит ошибки подписанной отгрузки: сентинеллы signedops, затем
// ошибки поиска ключа в passkeys, затем ошибки сервиса shipping.
func (h *Handler) writeShipError(w http.ResponseWriter, err error) {
	if signedops.WriteError(w, err) {
		return
	}
	switch {
	case errors.Is(err, passkeys.ErrPasskeyNotFound), errors.Is(err, passkeys.ErrSignerKeyNotActive):
		writeError(w, http.StatusConflict, "SIGNER_KEY_NOT_ACTIVE", "Ключ оператора не активен в реестре")
	default:
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
	}
}
