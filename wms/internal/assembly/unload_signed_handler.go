package assembly

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"wms/internal/passkeys"
	"wms/internal/platform/httputil"
	"wms/internal/signedops"
)

const (
	maxUnloadDraftBodyBytes  = 4 << 10
	maxUnloadSubmitBodyBytes = 256 << 10
)

type unloadDraftRequest struct {
	BufferBinID string `json:"buffer_bin_id"`
}

// DraftUnloadToShippingBuffer returns the multi-part signed draft of a
// basket→shipping-buffer pick.
func (h *Handler) DraftUnloadToShippingBuffer(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUnloadDraftBodyBytes)
	var req unloadDraftRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}

	bufferBinID, err := uuid.Parse(req.BufferBinID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "buffer_bin_id должен быть UUID")
		return
	}

	op, rpID, err := h.svc.DraftUnloadToShippingBuffer(r.Context(), operatorID, bufferBinID)
	if err != nil {
		h.writeUnloadError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, signedops.NewDraftView(op, rpID))
}

// SubmitUnloadToShippingBuffer verifies the passkey signatures of every part and
// applies the whole unload operation.
func (h *Handler) SubmitUnloadToShippingBuffer(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUnloadSubmitBodyBytes)
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

	result, err := h.svc.SubmitUnloadToShippingBuffer(r.Context(), operatorID, operationID, assertions)
	if err != nil {
		h.writeUnloadError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// writeUnloadError maps the signed-unload errors: package sentinels first, then
// the signer lookup errors of passkeys, then the assembly service errors.
func (h *Handler) writeUnloadError(w http.ResponseWriter, err error) {
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
