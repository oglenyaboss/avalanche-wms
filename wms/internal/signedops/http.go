package signedops

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/platform/httputil"
	"wms/internal/signedaction"
)

const msgInvalidRequest = "Некорректный запрос"

// DraftView is what the operator's browser needs to sign every part of an operation.
type DraftView struct {
	OperationID      string                `json:"operation_id"`
	ActionType       int                   `json:"action_type"`
	SignerKeyID      string                `json:"signer_key_id"`
	RPID             string                `json:"rp_id"`
	AllowCredentials []allowCredentialView `json:"allow_credentials"`
	UserVerification string                `json:"user_verification"`
	Timeout          int                   `json:"timeout"`
	Parts            []draftPartView       `json:"parts"`
}

type allowCredentialView struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type draftPartView struct {
	ActionID    string   `json:"action_id"`
	ItemIDs     []string `json:"item_ids"`
	DetailsHash string   `json:"details_hash"`
	Challenge   string   `json:"challenge"`
}

// NewDraftView renders the draft of an operation for the signing browser.
func NewDraftView(op *Operation, rpID string) DraftView {
	parts := make([]draftPartView, len(op.Parts))
	for i, p := range op.Parts {
		itemIDs := make([]string, len(p.ItemIDs))
		for j, id := range p.ItemIDs {
			itemIDs[j] = id.String()
		}
		parts[i] = draftPartView{
			ActionID:    p.ActionID.String(),
			ItemIDs:     itemIDs,
			DetailsHash: signedaction.Hex32(p.DetailsHash),
			Challenge:   base64.RawURLEncoding.EncodeToString(p.Challenge[:]),
		}
	}
	return DraftView{
		OperationID: op.OperationID.String(),
		ActionType:  int(op.ActionType),
		SignerKeyID: signedaction.Hex32(op.SignerKeyID),
		RPID:        rpID,
		AllowCredentials: []allowCredentialView{{
			Type: "public-key",
			ID:   base64.RawURLEncoding.EncodeToString(op.SignerCredentialID),
		}},
		UserVerification: "required",
		Timeout:          60000,
		Parts:            parts,
	}
}

// SubmitRequest is the body of the operation submit request: one assertion per part.
type SubmitRequest struct {
	OperationID string `json:"operation_id"`
	Assertions  []struct {
		ActionID string `json:"action_id"`
		signedaction.AssertionRequest
	} `json:"assertions"`
}

// Decode parses the operation id and the per-part assertions. Bad uuids, bad
// base64 and duplicate action ids all map to ErrInvalidRequest.
func (r SubmitRequest) Decode() (uuid.UUID, map[uuid.UUID]signedaction.Assertion, error) {
	operationID, err := uuid.Parse(r.OperationID)
	if err != nil {
		return uuid.UUID{}, nil, fmt.Errorf("%w: operation_id", ErrInvalidRequest)
	}
	out := make(map[uuid.UUID]signedaction.Assertion, len(r.Assertions))
	for _, a := range r.Assertions {
		actionID, err := uuid.Parse(a.ActionID)
		if err != nil {
			return uuid.UUID{}, nil, fmt.Errorf("%w: action_id", ErrInvalidRequest)
		}
		if _, dup := out[actionID]; dup {
			return uuid.UUID{}, nil, fmt.Errorf("%w: duplicate action_id", ErrInvalidRequest)
		}
		assertion, err := a.Decode()
		if err != nil {
			return uuid.UUID{}, nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		out[actionID] = assertion
	}
	return operationID, out, nil
}

// WriteError maps this package's sentinel errors to HTTP error envelopes and
// reports whether the error was handled.
func WriteError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrOperationNotFound):
		httputil.WriteError(w, http.StatusNotFound, "OPERATION_NOT_FOUND", "Операция не найдена")
	case errors.Is(err, ErrOperationSubmitted):
		httputil.WriteError(w, http.StatusConflict, "OPERATION_ALREADY_SUBMITTED", "Операция уже отправлена")
	case errors.Is(err, ErrOperationNotSubmitted):
		httputil.WriteError(w, http.StatusConflict, "OPERATION_NOT_SUBMITTED", "Операция ещё не отправлена")
	case errors.Is(err, ErrSignerNotActive):
		httputil.WriteError(w, http.StatusConflict, "SIGNER_KEY_NOT_ACTIVE", "Ключ подписанта не активен в реестре")
	case errors.Is(err, ErrInvalidAssertion):
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_ASSERTION", "Подтверждение не прошло проверку")
	case errors.Is(err, ErrInvalidRequest):
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
	default:
		return false
	}
	return true
}

// Handler serves the read side of signed warehouse operations. Drafts and
// submits live in the operations that use this package (receiving, …).
type Handler struct {
	repo *Repository
}

func NewHandler(repo *Repository) *Handler {
	return &Handler{repo: repo}
}

func (h *Handler) RegisterRoutes(r *mux.Router) {
	r.HandleFunc("/operations/{operationID}", h.OperationStatus).Methods(http.MethodGet)
}

type partStatusView struct {
	ActionID     string `json:"action_id"`
	State        string `json:"state"`
	RejectReason *int16 `json:"reject_reason"`
}

type operationStatusView struct {
	OperationID string           `json:"operation_id"`
	Parts       []partStatusView `json:"parts"`
}

func (h *Handler) OperationStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdminOrOperator(w, r)
	if !ok {
		return
	}
	operationID, err := uuid.Parse(mux.Vars(r)["operationID"])
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
		return
	}
	parts, err := h.repo.OperationStatus(r.Context(), operationID, userID)
	if err != nil {
		if !WriteError(w, err) {
			log.Printf("signedops: %v", err)
			httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
		}
		return
	}
	view := operationStatusView{OperationID: operationID.String(), Parts: make([]partStatusView, 0, len(parts))}
	for _, p := range parts {
		view.Parts = append(view.Parts, partStatusView{
			ActionID:     p.ActionID.String(),
			State:        string(p.State),
			RejectReason: p.RejectReason,
		})
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: view})
}
