package passkeys

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/domain"
	"wms/internal/platform/httputil"
	"wms/internal/signedaction"
)

const maxRegistrationBodyBytes = 64 << 10

const msgInvalidRequest = "Некорректный запрос"

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/registration/begin", h.BeginRegistration).Methods(http.MethodPost)
	router.HandleFunc("/registration/finish", h.FinishRegistration).Methods(http.MethodPost)
	router.HandleFunc("/me", h.MyPasskey).Methods(http.MethodGet)
	router.HandleFunc("/organization", h.Organization).Methods(http.MethodGet)
	router.HandleFunc("/organization/admin-key", h.AdminKeyExport).Methods(http.MethodGet)
	router.HandleFunc("/organization/keys/reject", h.RejectKey).Methods(http.MethodPost)
	router.HandleFunc("/actions/key-register/draft", h.DraftKeyRegister).Methods(http.MethodPost)
	router.HandleFunc("/actions/{actionID}/submit", h.SubmitKeyAction).Methods(http.MethodPost)
}

func (h *Handler) BeginRegistration(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdminOrOperator(w, r)
	if !ok {
		return
	}
	creation, err := h.svc.BeginRegistration(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: creation})
}

func (h *Handler) FinishRegistration(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdminOrOperator(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRegistrationBodyBytes)
	p, err := h.svc.FinishRegistration(r.Context(), userID, r.Body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, httputil.Envelope{Success: true, Data: toPasskeyView(&p)})
}

func (h *Handler) MyPasskey(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdminOrOperator(w, r)
	if !ok {
		return
	}
	p, err := h.svc.MyPasskey(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: toPasskeyView(p)})
}

func (h *Handler) Organization(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdmin(w, r)
	if !ok {
		return
	}
	overview, err := h.svc.Organization(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: toOrganizationView(overview)})
}

func (h *Handler) AdminKeyExport(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdmin(w, r)
	if !ok {
		return
	}
	export, err := h.svc.AdminKeyExport(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: export})
}

type draftKeyRegisterRequest struct {
	TargetKeyID string `json:"target_key_id"`
}

func (h *Handler) DraftKeyRegister(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdmin(w, r)
	if !ok {
		return
	}
	var body draftKeyRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
		return
	}
	draft, err := h.svc.DraftKeyRegister(r.Context(), userID, body.TargetKeyID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: toDraftView(draft)})
}

func (h *Handler) RejectKey(w http.ResponseWriter, r *http.Request) {
	userID, ok := httputil.RequireAdmin(w, r)
	if !ok {
		return
	}
	var body draftKeyRegisterRequest
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

type submitKeyActionRequest struct {
	CredentialID      string `json:"credential_id"`
	AuthenticatorData string `json:"authenticator_data"`
	ClientDataJSON    string `json:"client_data_json"`
	Signature         string `json:"signature"`
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
	var body submitKeyActionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
		return
	}
	assertion, err := decodeAssertion(body)
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

// decodeAssertion decodes the four base64url (no padding) body fields the way
// navigator.credentials.get returns them.
func decodeAssertion(body submitKeyActionRequest) (signedaction.Assertion, error) {
	decode := base64.RawURLEncoding.DecodeString
	credID, err := decode(body.CredentialID)
	if err != nil {
		return signedaction.Assertion{}, err
	}
	authData, err := decode(body.AuthenticatorData)
	if err != nil {
		return signedaction.Assertion{}, err
	}
	cdj, err := decode(body.ClientDataJSON)
	if err != nil {
		return signedaction.Assertion{}, err
	}
	sig, err := decode(body.Signature)
	if err != nil {
		return signedaction.Assertion{}, err
	}
	return signedaction.Assertion{
		CredentialID:      credID,
		AuthenticatorData: authData,
		ClientDataJSON:    cdj,
		Signature:         sig,
	}, nil
}

type passkeyView struct {
	CredentialID string `json:"credential_id"`
	KeyID        string `json:"key_id"`
	QX           string `json:"qx"`
	QY           string `json:"qy"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

func toPasskeyView(p *domain.Passkey) *passkeyView {
	if p == nil {
		return nil
	}
	return &passkeyView{
		CredentialID: base64.RawURLEncoding.EncodeToString(p.CredentialID),
		KeyID:        "0x" + hex.EncodeToString(p.KeyID[:]),
		QX:           "0x" + hex.EncodeToString(p.QX[:]),
		QY:           "0x" + hex.EncodeToString(p.QY[:]),
		Role:         string(p.Role),
		Status:       string(p.Status),
		CreatedAt:    p.CreatedAt.Format(time.RFC3339),
	}
}

type organizationInfoView struct {
	OrgID string `json:"org_id"`
	Name  string `json:"name"`
}

type memberView struct {
	UserID       string            `json:"user_id"`
	Username     string            `json:"username"`
	Role         string            `json:"role"`
	Passkey      *passkeyView      `json:"passkey"`
	Confirmation *confirmationView `json:"confirmation"`
}

type organizationView struct {
	Organization organizationInfoView `json:"organization"`
	Members      []memberView         `json:"members"`
}

func toOrganizationView(o OrganizationOverview) organizationView {
	members := make([]memberView, 0, len(o.Members))
	for _, m := range o.Members {
		members = append(members, memberView{
			UserID:       m.UserID.String(),
			Username:     m.Username,
			Role:         string(m.Role),
			Passkey:      toPasskeyView(m.Passkey),
			Confirmation: toConfirmationView(m.Confirmation),
		})
	}
	return organizationView{
		Organization: organizationInfoView{OrgID: o.Organization.ID.String(), Name: o.Organization.Name},
		Members:      members,
	}
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
			QX: "0x" + hex.EncodeToString(d.SubjectQX[:]),
			QY: "0x" + hex.EncodeToString(d.SubjectQY[:]),
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
	State        string `json:"state"`
	RejectReason *int16 `json:"reject_reason"`
}

func toConfirmationView(c *KeyConfirmation) *confirmationView {
	if c == nil {
		return nil
	}
	return &confirmationView{
		ActionID:     c.ActionID.String(),
		State:        string(c.State),
		RejectReason: c.RejectReason,
	}
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		httputil.WriteError(w, http.StatusForbidden, "FORBIDDEN", "Недостаточно прав для этого действия")
	case errors.Is(err, ErrNoOrganization):
		httputil.WriteError(w, http.StatusForbidden, "NO_ORGANIZATION", "Пользователь не принадлежит организации")
	case errors.Is(err, ErrPasskeyExists):
		httputil.WriteError(w, http.StatusConflict, "PASSKEY_EXISTS", "Ключ уже зарегистрирован")
	case errors.Is(err, ErrInvalidRegistration):
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REGISTRATION", "Некорректная регистрация passkey")
	case errors.Is(err, ErrPasskeyNotFound):
		httputil.WriteError(w, http.StatusNotFound, "PASSKEY_NOT_FOUND", "Ключ не найден")
	case errors.Is(err, ErrSignerKeyNotActive):
		httputil.WriteError(w, http.StatusConflict, "SIGNER_KEY_NOT_ACTIVE", "Ключ администратора не активен в реестре")
	case errors.Is(err, ErrTargetNotPending):
		httputil.WriteError(w, http.StatusConflict, "TARGET_NOT_PENDING", "Ключ оператора не ожидает подтверждения")
	case errors.Is(err, ErrActionInProgress):
		httputil.WriteError(w, http.StatusConflict, "ACTION_IN_PROGRESS", "Для ключа уже есть действие в работе")
	case errors.Is(err, ErrActionNotFound):
		httputil.WriteError(w, http.StatusNotFound, "ACTION_NOT_FOUND", "Действие не найдено")
	case errors.Is(err, ErrActionAlreadySubmitted):
		httputil.WriteError(w, http.StatusConflict, "ACTION_ALREADY_SUBMITTED", "Действие уже отправлено")
	case errors.Is(err, ErrInvalidAssertion):
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_ASSERTION", "Подтверждение не прошло проверку")
	case errors.Is(err, ErrInvalidRequest):
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidRequest)
	default:
		log.Printf("passkeys: %v", err)
		httputil.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Внутренняя ошибка сервера")
	}
}
