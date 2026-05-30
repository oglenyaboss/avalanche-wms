package auth

import (
	"context"

	"github.com/google/uuid"

	"wms/internal/domain"
)

// ContextWithIdentity returns a copy of ctx carrying the authenticated user's ID
// and role. It is the single writer of the identity context keys: Middleware
// calls it after verifying a JWT, and UserIDFromCtx / UserRoleFromCtx read the
// values back. The role is stored as a string so UserRoleFromCtx's type
// assertion reads it back consistently.
//
// Being the canonical injector, it also lets tests build an authenticated
// context for role-gated handlers without minting and signing a JWT.
func ContextWithIdentity(ctx context.Context, userID uuid.UUID, role domain.UserRole) context.Context {
	ctx = context.WithValue(ctx, ctxUserID, userID)
	return context.WithValue(ctx, ctxUserRole, string(role))
}
