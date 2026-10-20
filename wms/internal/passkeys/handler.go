package passkeys

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"wms/internal/domain"
	"wms/internal/platform/httputil"
)

const maxRegistrationBodyBytes = 64 << 10
const maxActionBodyBytes = 16 << 10

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
	router.HandleFunc("/actions/key-revoke/draft", h.DraftKeyRevoke).Methods(http.MethodPost)
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
		QX:           "0x" + hex.EncodeToString(p.Key.X[:]),
		QY:           "0x" + hex.EncodeToString(p.Key.Y[:]),
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
	case errors.Is(err, ErrTargetNotActive):
		httputil.WriteError(w, http.StatusConflict, "TARGET_NOT_ACTIVE", "Ключ оператора не активен в реестре")
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
