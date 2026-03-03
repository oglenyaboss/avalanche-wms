package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"wms/internal/domain"
)

func TestAuthMiddlewareValidBearerToken(t *testing.T) {
	secret := "test-secret"
	userID := uuid.New()
	token := mustSignClaims(t, secret, tokenClaims{
		UserID: userID.String(),
		Role:   string(domain.UserRoleAdmin),
		Type:   tokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
		},
	})

	nextCalled := false
	var gotUserID uuid.UUID
	var gotRole domain.UserRole
	// The next handler will check if the user ID and role from the context match what we expect
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		gotUserID = UserIDFromCtx(r.Context())
		gotRole = UserRoleFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/private", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	Middleware(secret)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if !nextCalled {
		t.Fatal("expected next handler to be called")
	}
	if gotUserID != userID {
		t.Fatalf("expected userID %s, got %s", userID, gotUserID)
	}
	if gotRole != domain.UserRoleAdmin {
		t.Fatalf("expected role %s, got %s", domain.UserRoleAdmin, gotRole)
	}
}

func TestAuthMiddlewareMissingAuthorizationHeader(t *testing.T) {
	secret := "test-secret"

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/private", http.NoBody)
	rr := httptest.NewRecorder()

	Middleware(secret)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rr.Code)
	}
	if nextCalled {
		t.Fatal("expected next handler not to be called")
	}
}

func TestAuthMiddlewareInvalidSignature(t *testing.T) {
	secret := "test-secret"
	userID := uuid.New()
	token := mustSignClaims(t, "another-secret", tokenClaims{
		UserID: userID.String(),
		Role:   string(domain.UserRoleAdmin),
		Type:   tokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
		},
	})

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/private", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	Middleware(secret)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rr.Code)
	}
	if nextCalled {
		t.Fatal("expected next handler not to be called")
	}
}

func TestAuthMiddlewareExpiredToken(t *testing.T) {
	secret := "test-secret"
	userID := uuid.New()
	token := mustSignClaims(t, secret, tokenClaims{
		UserID: userID.String(),
		Role:   string(domain.UserRoleAdmin),
		Type:   tokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	})

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/private", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	Middleware(secret)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rr.Code)
	}
	if nextCalled {
		t.Fatal("expected next handler not to be called")
	}
}
