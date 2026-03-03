package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"wms/internal/domain"
)

type mockUserRepo struct {
	usersByUsername map[string]*domain.User
	usersByID       map[uuid.UUID]*domain.User
	createErr       error
	lastCreatedUser *domain.User
}

func (m *mockUserRepo) CreateUser(_ context.Context, user *domain.User) error {
	if m.createErr != nil {
		return m.createErr
	}

	if m.usersByUsername == nil {
		m.usersByUsername = make(map[string]*domain.User)
	}
	if m.usersByID == nil {
		m.usersByID = make(map[uuid.UUID]*domain.User)
	}

	m.usersByUsername[user.Username] = user
	m.usersByID[user.ID] = user
	m.lastCreatedUser = user

	return nil
}

func (m *mockUserRepo) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	user, ok := m.usersByUsername[username]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return user, nil
}

func (m *mockUserRepo) GetUserByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	user, ok := m.usersByID[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return user, nil
}

func TestServiceLoginSuccess(t *testing.T) {
	passwordHash := mustHashPassword(t, "password123")
	userID := uuid.New()
	repo := &mockUserRepo{
		usersByUsername: map[string]*domain.User{
			"admin": {
				ID:           userID,
				Username:     "admin",
				PasswordHash: passwordHash,
				Role:         domain.UserRoleAdmin,
				IsActive:     true,
			},
		},
	}
	svc := NewService(repo, "test-secret", 15*time.Minute, 7*24*time.Hour)

	pair, err := svc.Login(context.Background(), "admin", "password123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("expected non-empty access and refresh tokens")
	}
}

func TestServiceLoginWrongPassword(t *testing.T) {
	passwordHash := mustHashPassword(t, "password123")
	userID := uuid.New()
	repo := &mockUserRepo{
		usersByUsername: map[string]*domain.User{
			"admin": {
				ID:           userID,
				Username:     "admin",
				PasswordHash: passwordHash,
				Role:         domain.UserRoleAdmin,
				IsActive:     true,
			},
		},
	}
	svc := NewService(repo, "test-secret", 15*time.Minute, 7*24*time.Hour)

	_, err := svc.Login(context.Background(), "admin", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestServiceLoginUserNotFound(t *testing.T) {
	repo := &mockUserRepo{}
	svc := NewService(repo, "test-secret", 15*time.Minute, 7*24*time.Hour)

	_, err := svc.Login(context.Background(), "missing", "password123")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestServiceRefreshValidToken(t *testing.T) {
	userID := uuid.New()
	repo := &mockUserRepo{
		usersByID: map[uuid.UUID]*domain.User{
			userID: {
				ID:       userID,
				Username: "operator",
				Role:     domain.UserRoleOperator,
				IsActive: true,
			},
		},
	}
	svc := NewService(repo, "test-secret", 15*time.Minute, 7*24*time.Hour)
	// Generate a valid refresh token for the user
	refreshToken, err := svc.generateRefreshToken(userID)
	if err != nil {
		t.Fatalf("generateRefreshToken: %v", err)
	}

	pair, err := svc.Refresh(context.Background(), refreshToken)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("expected non-empty access and refresh tokens")
	}
}

func TestServiceRefreshExpiredToken(t *testing.T) {
	userID := uuid.New()
	repo := &mockUserRepo{
		usersByID: map[uuid.UUID]*domain.User{
			userID: {
				ID:       userID,
				Username: "operator",
				Role:     domain.UserRoleOperator,
				IsActive: true,
			},
		},
	}
	svc := NewService(repo, "test-secret", 15*time.Minute, 7*24*time.Hour)

	expiredToken := mustSignClaims(t, "test-secret", tokenClaims{
		UserID: userID.String(),
		Type:   tokenTypeRefresh,
		RegisteredClaims: jwt.RegisteredClaims{
			// Set IssuedAt and ExpiresAt in the past to simulate an expired token
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	})

	_, err := svc.Refresh(context.Background(), expiredToken)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestServiceRefreshAccessTokenInsteadOfRefresh(t *testing.T) {
	userID := uuid.New()
	repo := &mockUserRepo{
		usersByID: map[uuid.UUID]*domain.User{
			userID: {
				ID:       userID,
				Username: "operator",
				Role:     domain.UserRoleOperator,
				IsActive: true,
			},
		},
	}
	svc := NewService(repo, "test-secret", 15*time.Minute, 7*24*time.Hour)

	accessToken, err := svc.generateAccessToken(userID, domain.UserRoleOperator)
	if err != nil {
		t.Fatalf("generateAccessToken: %v", err)
	}

	_, err = svc.Refresh(context.Background(), accessToken)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestServiceRegisterFromAdmin(t *testing.T) {
	repo := &mockUserRepo{}
	svc := NewService(repo, "test-secret", 15*time.Minute, 7*24*time.Hour)

	user, err := svc.Register(
		context.Background(),
		domain.UserRoleAdmin,
		"new-operator",
		"password123",
		domain.UserRoleOperator,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if user.ID == uuid.Nil {
		t.Fatal("expected user ID to be generated")
	}
	if user.Username != "new-operator" {
		t.Fatalf("unexpected username: %s", user.Username)
	}
	if repo.lastCreatedUser == nil {
		t.Fatal("expected repository CreateUser to be called")
	}
	if repo.lastCreatedUser.PasswordHash == "password123" {
		t.Fatal("expected password to be hashed")
	}
}

func TestServiceRegisterFromNonAdmin(t *testing.T) {
	repo := &mockUserRepo{}
	svc := NewService(repo, "test-secret", 15*time.Minute, 7*24*time.Hour)

	_, err := svc.Register(
		context.Background(),
		domain.UserRoleOperator,
		"new-user",
		"password123",
		domain.UserRoleCustomer,
	)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func mustHashPassword(t *testing.T, password string) string {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}

	return string(hash)
}

func mustSignClaims(t *testing.T, secret string, claims tokenClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	return signed
}
