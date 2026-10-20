package putaway

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
	maxPlaceDraftBodyBytes  = 16 << 10
	maxPlaceSubmitBodyBytes = 256 << 10
)

type placeDraftRequest struct {
	ProductIDs   []string `json:"product_ids"`
	StorageBinID string   `json:"storage_bin_id"`
}

// DraftPlaceToStorageBin returns the multi-part signed draft of a
// basket→storage-bin putaway.
func (h *Handler) DraftPlaceToStorageBin(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPlaceDraftBodyBytes)
	var req placeDraftRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	if len(req.ProductIDs) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "product_ids не может быть пустым")
		return
	}
	if len(req.ProductIDs) > maxProductIDsPerRequest {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("product_ids не может содержать более %d элементов", maxProductIDsPerRequest))
		return
	}

	productIDs := make([]uuid.UUID, 0, len(req.ProductIDs))
	for _, idStr := range req.ProductIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "product_id должен быть UUID")
			return
		}
		productIDs = append(productIDs, id)
	}

	storageBinID, err := uuid.Parse(req.StorageBinID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "storage_bin_id должен быть UUID")
		return
	}

	op, rpID, err := h.svc.DraftPlaceToStorageBin(r.Context(), operatorID, productIDs, storageBinID)
	if err != nil {
		h.writePlaceError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, signedops.NewDraftView(op, rpID))
}

// SubmitPlaceToStorageBin verifies the passkey signatures of every part and
// applies the whole putaway operation.
func (h *Handler) SubmitPlaceToStorageBin(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPlaceSubmitBodyBytes)
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

	result, err := h.svc.SubmitPlaceToStorageBin(r.Context(), operatorID, operationID, assertions)
	if err != nil {
		h.writePlaceError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// writePlaceError maps the signed-putaway errors: package sentinels first, then
// the signer lookup errors of passkeys, then the putaway service errors.
func (h *Handler) writePlaceError(w http.ResponseWriter, err error) {
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
