package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wms/internal/domain"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateUser(ctx context.Context, user *domain.User) error {
	_, err := r.db.Exec(
		ctx,
		`INSERT INTO public.users (user_id, username, password_hash, role, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		user.ID,
		user.Username,
		user.PasswordHash,
		string(user.Role),
		user.IsActive,
		user.CreatedAt,
		user.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("auth.Repository.CreateUser exec: %w", err)
	}

	return nil
}

func (r *Repository) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	const query = `SELECT user_id, username, password_hash, role, is_active, created_at, updated_at
		FROM public.users
		WHERE username = $1`

	var user domain.User
	// DB stores role as string, convert to domain.UserRole after scanning
	var role string

	err := r.db.QueryRow(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&role,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("auth.Repository.GetUserByUsername scan: %w", err)
	}

	user.Role = domain.UserRole(role)
	return &user, nil
}

func (r *Repository) GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	const query = `SELECT user_id, username, password_hash, role, is_active, created_at, updated_at
		FROM public.users
		WHERE user_id = $1`

	var user domain.User
	var role string

	err := r.db.QueryRow(ctx, query, id).Scan(
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&role,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("auth.Repository.GetUserByID scan: %w", err)
	}

	user.Role = domain.UserRole(role)
	return &user, nil
}
