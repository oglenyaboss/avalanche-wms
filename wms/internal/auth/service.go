package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"wms/internal/domain"
)

const (
	tokenTypeAccess  = "access"
	tokenTypeRefresh = "refresh"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")
	ErrForbidden          = errors.New("forbidden")
	ErrInvalidInput       = errors.New("invalid input")
)

type UserRepository interface {
	CreateUser(ctx context.Context, user *domain.User) error
	GetUserByUsername(ctx context.Context, username string) (*domain.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
}

type tokenClaims struct {
	UserID string `json:"user_id"`
	Role   string `json:"role,omitempty"`
	Type   string `json:"type"`
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type Service struct {
	repo       UserRepository
	jwtSecret  []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewService(repo UserRepository, jwtSecret string, accessTTL, refreshTTL time.Duration) *Service {
	return &Service{
		repo:       repo,
		jwtSecret:  []byte(jwtSecret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

func (s *Service) Login(ctx context.Context, username, password string) (*TokenPair, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("auth.Service.Login: %w", ErrInvalidCredentials)
	}

	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("auth.Service.Login: %w", ErrInvalidCredentials)
		}
		return nil, fmt.Errorf("auth.Service.Login get user: %w", err)
	}

	if !user.IsActive {
		return nil, fmt.Errorf("auth.Service.Login: %w", ErrInvalidCredentials)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("auth.Service.Login: %w", ErrInvalidCredentials)
	}

	pair, err := s.generateTokenPair(user.ID, user.Role)
	if err != nil {
		return nil, fmt.Errorf("auth.Service.Login generate tokens: %w", err)
	}

	return pair, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	// Only accept refresh token
	claims, err := parseToken(refreshToken, s.jwtSecret, tokenTypeRefresh)
	if err != nil {
		return nil, fmt.Errorf("auth.Service.Refresh: %w", err)
	}
	// The user ID is stored as a string in the token claims, so we need to parse it back to a UUID.
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("auth.Service.Refresh parse user id: %w", err)
	}

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("auth.Service.Refresh: %w", ErrInvalidToken)
		}
		return nil, fmt.Errorf("auth.Service.Refresh get user: %w", err)
	}

	if !user.IsActive {
		return nil, fmt.Errorf("auth.Service.Refresh: %w", ErrInvalidToken)
	}

	pair, err := s.generateTokenPair(user.ID, user.Role)
	if err != nil {
		return nil, fmt.Errorf("auth.Service.Refresh generate tokens: %w", err)
	}

	return pair, nil
}

func (s *Service) Register(ctx context.Context,
	actorRole domain.UserRole,
	username, password string,
	role domain.UserRole,
) (*domain.User, error) {
	if actorRole != domain.UserRoleAdmin {
		return nil, fmt.Errorf("auth.Service.Register: %w", ErrForbidden)
	}

	if username == "" || password == "" || !isValidRole(role) {
		return nil, fmt.Errorf("auth.Service.Register: %w", ErrInvalidInput)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("auth.Service.Register hash password: %w", err)
	}

	now := time.Now().UTC()
	user := &domain.User{
		ID:           uuid.New(),
		Username:     username,
		PasswordHash: string(passwordHash),
		Role:         role,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.repo.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("auth.Service.Register create user: %w", err)
	}

	return user, nil
}

func parseToken(tokenString string, jwtSecret []byte, expectedType string) (*tokenClaims, error) {
	claims := &tokenClaims{}
	// ParseWithClaims will validate the token and return an error if it's invalid.
	// also check the signing method to prevent certain attacks.
	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("%w: unexpected signing method", ErrInvalidToken)
			}
			return jwtSecret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("%w: parse token", ErrInvalidToken)
	}

	if claims.Type != expectedType {
		return nil, fmt.Errorf("%w: invalid token type", ErrInvalidToken)
	}

	return claims, nil
}

// generateTokenPair creates a new access and refresh token pair for the given user ID and role.
func (s *Service) generateTokenPair(userID uuid.UUID, role domain.UserRole) (*TokenPair, error) {
	accessToken, err := s.generateAccessToken(userID, role)
	if err != nil {
		return nil, fmt.Errorf("auth.Service.generateTokenPair access: %w", err)
	}

	refreshToken, err := s.generateRefreshToken(userID)
	if err != nil {
		return nil, fmt.Errorf("auth.Service.generateTokenPair refresh: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (s *Service) generateAccessToken(userID uuid.UUID, role domain.UserRole) (string, error) {
	now := time.Now().UTC()
	claims := tokenClaims{
		UserID: userID.String(),
		Role:   string(role),
		Type:   tokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("auth.Service.generateAccessToken sign: %w", err)
	}
	return signed, nil
}

func (s *Service) generateRefreshToken(userID uuid.UUID) (string, error) {
	now := time.Now().UTC()
	claims := tokenClaims{
		UserID: userID.String(),
		Type:   tokenTypeRefresh,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.refreshTTL)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("auth.Service.generateRefreshToken sign: %w", err)
	}
	return signed, nil
}

func isValidRole(role domain.UserRole) bool {
	switch role {
	case domain.UserRoleAdmin, domain.UserRoleOperator, domain.UserRoleCustomer:
		return true
	default:
		return false
	}
}
