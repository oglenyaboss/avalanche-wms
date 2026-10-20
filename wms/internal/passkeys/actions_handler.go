package passkeys

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/platform/httputil"
	"wms/internal/signedaction"
)

type targetKeyRequest struct {
	TargetKeyID string `json:"target_key_id"`
}

func (h *Handler) DraftKeyRegister(w http.ResponseWriter, r *http.Request) {
	h.draftKeyAction(h.svc.DraftKeyRegister)(w, r)
}

func (h *Handler) DraftKeyRevoke(w http.ResponseWriter, r *http.Request) {
	h.draftKeyAction(h.svc.DraftKeyRevoke)(w, r)
}

// draftKeyAction serves both key-action draft endpoints: admin-only, a small
// body with the target key id, one draft view as the response.
func (h *Handler) draftKeyAction(draft func(ctx context.Context, userID uuid.UUID, targetKeyID string) (*KeyActionDraft, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := httputil.RequireAdmin(w, r)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxActionBodyBytes)
		var body targetKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
			return
		}
		d, err := draft(r.Context(), userID, body.TargetKeyID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: toDraftView(d)})
	}
}

func (h *Handler) RejectKey(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdmin(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxActionBodyBytes)
	var body targetKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
		return
	}
	if err := h.svc.RejectKey(r.Context(), userID, body.TargetKeyID); err != nil {
		writeServiceError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true})
}

func (h *Handler) SubmitKeyAction(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdmin(w, r)
	if !ok {
		return
	}
	actionID, err := uuid.Parse(mux.Vars(r)["actionID"])
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxActionBodyBytes)
	var body signedaction.AssertionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
		return
	}
	assertion, err := body.Decode()
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
		return
	}
	confirmation, err := h.svc.SubmitKeyAction(r.Context(), userID, actionID, assertion)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: toConfirmationView(confirmation)})
}

type coordinateView struct {
	QX string `json:"qx"`
	QY string `json:"qy"`
}

type allowCredentialView struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type keyActionDraftView struct {
	ActionID         string                `json:"action_id"`
	ActionType       int                   `json:"action_type"`
	ItemIDs          []string              `json:"item_ids"`
	SubjectKey       coordinateView        `json:"subject_key"`
	DetailsHash      string                `json:"details_hash"`
	SignerKeyID      string                `json:"signer_key_id"`
	Challenge        string                `json:"challenge"`
	RPID             string                `json:"rp_id"`
	AllowCredentials []allowCredentialView `json:"allow_credentials"`
	UserVerification string                `json:"user_verification"`
	Timeout          int                   `json:"timeout"`
}

func toDraftView(d *KeyActionDraft) keyActionDraftView {
	return keyActionDraftView{
		ActionID:   d.Action.ActionID.String(),
		ActionType: int(d.Action.ActionType),
		ItemIDs:    []string{},
		SubjectKey: coordinateView{
			QX: "0x" + hex.EncodeToString(d.Subject.X[:]),
			QY: "0x" + hex.EncodeToString(d.Subject.Y[:]),
		},
		DetailsHash:      "0x" + hex.EncodeToString(d.DetailsHash[:]),
		SignerKeyID:      "0x" + hex.EncodeToString(d.SignerKeyID[:]),
		Challenge:        base64.RawURLEncoding.EncodeToString(d.Action.Challenge[:]),
		RPID:             d.RPID,
		AllowCredentials: []allowCredentialView{{Type: "public-key", ID: base64.RawURLEncoding.EncodeToString(d.Action.SignerCredentialID)}},
		UserVerification: "required",
		Timeout:          60000,
	}
}

type confirmationView struct {
	ActionID     string `json:"action_id"`
	ActionType   uint8  `json:"action_type"`
	State        string `json:"state"`
	RejectReason *int16 `json:"reject_reason"`
}

func toConfirmationView(c *KeyConfirmation) *confirmationView {
	if c == nil {
		return nil
	}
	return &confirmationView{
		ActionID:     c.ActionID.String(),
		ActionType:   c.ActionType,
		State:        string(c.State),
		RejectReason: c.RejectReason,
	}
}
