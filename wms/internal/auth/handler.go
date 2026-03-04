package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/domain"
)

type ctxKey string

const (
	ctxUserID   ctxKey = "user_id"
	ctxUserRole ctxKey = "role"
)

type Handler struct {
	svc *Service
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type registerResponse struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes registers the auth-related routes to the provided router.
func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/auth/login", h.Login).Methods(http.MethodPost)
	router.HandleFunc("/auth/refresh", h.Refresh).Methods(http.MethodPost)
	router.Handle(
		"/auth/register",
		Middleware(h.svc.jwtSecret)(http.HandlerFunc(h.Register)),
	).Methods(http.MethodPost)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	pair, err := h.svc.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, pair)
}

// Refresh handles token refresh requests.
// It expects a valid refresh token in the request body and returns a new token pair if the refresh token is valid.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	pair, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrInvalidToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, pair)
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	actorRole := UserRoleFromCtx(r.Context())

	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := h.svc.Register(
		r.Context(),
		actorRole,
		req.Username,
		req.Password,
		domain.UserRole(req.Role),
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrForbidden):
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		case errors.Is(err, ErrUserExists):
			http.Error(w, "user already exists", http.StatusConflict)
			return
		case errors.Is(err, ErrInvalidInput):
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		default:
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusCreated, registerResponse{
		ID:       user.ID,
		Username: user.Username,
	})
}

func Middleware(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString, err := extractBearerToken(r.Header.Get("Authorization"))
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			claims, err := parseToken(tokenString, jwtSecret, tokenTypeAccess)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			userID, err := uuid.Parse(claims.UserID)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), ctxUserID, userID)
			ctx = context.WithValue(ctx, ctxUserRole, claims.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserIDFromCtx(ctx context.Context) uuid.UUID {
	id, ok := ctx.Value(ctxUserID).(uuid.UUID)
	if !ok {
		return uuid.Nil
	}
	return id
}

func UserRoleFromCtx(ctx context.Context) domain.UserRole {
	role, ok := ctx.Value(ctxUserRole).(string)
	if !ok {
		return ""
	}
	return domain.UserRole(role)
}

// decodeJSON decodes the JSON body of the request into the provided destination struct.
// It also disallows unknown fields to prevent silent errors.
func decodeJSON(r *http.Request, dest any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("auth.writeJSON encode: %v", err)
	}
}

func extractBearerToken(header string) (string, error) {
	if header == "" {
		return "", errors.New("missing authorization header")
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		return "", errors.New("invalid authorization header")
	}

	return parts[1], nil
}
