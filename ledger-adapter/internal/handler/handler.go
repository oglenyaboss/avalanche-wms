package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

// RegistryReader is the ConsortiumRegistry read surface for /registry/keys.
// *chain.Registry satisfies it.
type RegistryReader interface {
	GetKey(ctx context.Context, keyID [32]byte) (chain.RegistryKey, error)
}

// Handler holds HTTP handler state: /health (liveness) and /registry/keys
// (key status).
type Handler struct {
	registry RegistryReader
}

// New creates a Handler. registry may be nil if the process runs without it
// (then /registry/keys returns 502); /health never depends on it.
func New(registry RegistryReader) *Handler {
	return &Handler{registry: registry}
}

// RegisterRoutes wires /health (liveness) and the registry key status.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.Health)
	mux.HandleFunc("GET /registry/keys/{keyId}", h.RegistryKey)
}

// Health отвечает 200 если процесс жив.
// TODO(#50): add readiness probe checking pool/Kafka/RPC reachability
func (h *Handler) Health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Игнорируем ошибку Encode — это network hiccup при записи health-ответа,
	// не fatal. Старая версия делала log.Fatalf что убило бы процесс.
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

var keyIDRe = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

type registryKeyResponse struct {
	Found  bool   `json:"found"`
	OrgID  string `json:"org_id"`
	Role   string `json:"role"`
	Active bool   `json:"active"`
}

// RegistryKey — статус ключа в ConsortiumRegistry; без аутентификации
// (только внутренняя сеть и loopback хоста).
func (h *Handler) RegistryKey(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("keyId")
	if !keyIDRe.MatchString(raw) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "INVALID_KEY_ID"})
		return
	}
	if h.registry == nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CHAIN_UNREACHABLE"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	key, err := h.registry.GetKey(ctx, [32]byte(common.HexToHash(raw)))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CHAIN_UNREACHABLE"})
		return
	}
	if key.Role == chain.RoleNone {
		writeJSON(w, http.StatusOK, map[string]bool{"found": false})
		return
	}
	role := "operator"
	if key.Role == chain.RoleAdmin {
		role = "admin"
	}
	writeJSON(w, http.StatusOK, registryKeyResponse{
		Found:  true,
		OrgID:  uuid.UUID(key.OrgID).String(),
		Role:   role,
		Active: key.Active,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
