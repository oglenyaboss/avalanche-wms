package httputil

import (
	"net/http"
	"slices"

	"github.com/google/uuid"

	"wms/internal/auth"
	"wms/internal/domain"
)

const (
	msgUnauthenticated = "Требуется авторизация"
	msgOperatorOnly    = "Только оператор может выполнять это действие"
	msgAdminOnly       = "Только администратор может выполнять это действие"
	msgAdminOrOperator = "Требуется роль администратора или оператора"
	msgAuditorOrAdmin  = "Требуется роль аудитора или администратора"
)

// RequireOperator authorizes the request for the OPERATOR role only. It is used
// by line-execution endpoints (scan, pick, ship). On failure it writes the
// standard error envelope — 401 UNAUTHORIZED when unauthenticated, 403 FORBIDDEN
// otherwise — and returns ok=false. On success it returns the operator's user ID.
func RequireOperator(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return requireRole(w, r, msgOperatorOnly, domain.UserRoleOperator)
}

// RequireAdmin authorizes the request for the ADMIN role only.
func RequireAdmin(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return requireRole(w, r, msgAdminOnly, domain.UserRoleAdmin)
}

// RequireAdminOrOperator authorizes the request for the ADMIN or OPERATOR roles.
// Management endpoints (e.g. assembly allocation) use it so an administrator can
// perform coordination actions without provisioning a separate operator account.
// Behavior is otherwise identical to RequireOperator.
func RequireAdminOrOperator(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return requireRole(w, r, msgAdminOrOperator, domain.UserRoleAdmin, domain.UserRoleOperator)
}

// RequireAuditorOrAdmin authorizes the request for the AUDITOR or ADMIN roles.
// Read-only audit endpoints use it; every other gate stays role-specific, so an
// auditor cannot call any mutating or operational endpoint.
// Behavior is otherwise identical to RequireOperator.
func RequireAuditorOrAdmin(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return requireRole(w, r, msgAuditorOrAdmin, domain.UserRoleAuditor, domain.UserRoleAdmin)
}

// requireRole is the shared gate: it requires an authenticated user whose role is
// in allowed. forbiddenMsg is the user-facing message for the 403 response.
func requireRole(w http.ResponseWriter, r *http.Request, forbiddenMsg string, allowed ...domain.UserRole) (uuid.UUID, bool) {
	userID := auth.UserIDFromCtx(r.Context())
	if userID == uuid.Nil {
		WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", msgUnauthenticated)
		return uuid.Nil, false
	}

	role := auth.UserRoleFromCtx(r.Context())
	if slices.Contains(allowed, role) {
		return userID, true
	}

	WriteError(w, http.StatusForbidden, "FORBIDDEN", forbiddenMsg)
	return uuid.Nil, false
}
