package receiving

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"wms/internal/passkeys"
	"wms/internal/platform/httputil"
	"wms/internal/signedops"
)

const (
	maxCloseDraftBodyBytes  = 4 << 10
	maxCloseSubmitBodyBytes = 256 << 10
)

type closeCargoplaceDraftRequest struct {
	CargoplaceID string `json:"cargoplace_id"`
}

// DraftCloseCargoplace returns the multi-part signed draft of a cargoplace close.
func (h *Handler) DraftCloseCargoplace(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCloseDraftBodyBytes)
	var req closeCargoplaceDraftRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Невалидный JSON в теле запроса")
		return
	}
	cargoplaceID, err := uuid.Parse(req.CargoplaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "cargoplace_id должен быть UUID")
		return
	}

	op, rpID, err := h.svc.DraftCloseCargoplace(r.Context(), operatorID, cargoplaceID)
	if err != nil {
		h.writeCloseError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, signedops.NewDraftView(op, rpID))
}

// SubmitCloseCargoplace verifies the passkey signatures of every part and
// applies the whole close operation.
func (h *Handler) SubmitCloseCargoplace(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httputil.RequireOperator(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCloseSubmitBodyBytes)
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

	result, err := h.svc.SubmitCloseCargoplace(r.Context(), operatorID, operationID, assertions)
	if err != nil {
		h.writeCloseError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, result)
}

// writeCloseError maps the signed-close errors: package sentinels first, then
// the signer lookup errors of passkeys, then the receiving service errors.
func (h *Handler) writeCloseError(w http.ResponseWriter, err error) {
	if signedops.WriteError(w, err) {
		return
	}
	switch {
	case errors.Is(err, passkeys.ErrRegistryUnavailable):
		writeError(w, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Реестр недоступен, повторите позже")
	case errors.Is(err, passkeys.ErrPasskeyNotFound), errors.Is(err, passkeys.ErrSignerKeyNotActive):
		writeError(w, http.StatusConflict, "SIGNER_KEY_NOT_ACTIVE", "Ключ оператора не активен в реестре")
	default:
		status, code, message := mapServiceError(err)
		writeError(w, status, code, message)
	}
}
