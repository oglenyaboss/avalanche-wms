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
	mux.HandleFunc("POST /registry/keys", h.RegistryKeys)
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
	writeJSON(w, http.StatusOK, registryKeyView(key))
}

// registryKeyView отдаёт JSON-вид ключа реестра; для ключа без роли — тот же
// {"found":false}, что и в GET.
func registryKeyView(key chain.RegistryKey) any {
	if key.Role == chain.RoleNone {
		return map[string]bool{"found": false}
	}
	role := "operator"
	if key.Role == chain.RoleAdmin {
		role = "admin"
	}
	return registryKeyResponse{
		Found:  true,
		OrgID:  uuid.UUID(key.OrgID).String(),
		Role:   role,
		Active: key.Active,
	}
}

// RegistryKeys — статусы нескольких ключей одним запросом (пакетный вариант GET).
func (h *Handler) RegistryKeys(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KeyIDs []string `json:"key_ids"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "INVALID_BODY"})
		return
	}
	if len(body.KeyIDs) == 0 || len(body.KeyIDs) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "INVALID_KEY_IDS"})
		return
	}
	for _, id := range body.KeyIDs {
		if !keyIDRe.MatchString(id) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "INVALID_KEY_ID"})
			return
		}
	}
	if h.registry == nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CHAIN_UNREACHABLE"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	keys := make(map[string]any, len(body.KeyIDs))
	for _, id := range body.KeyIDs {
		key, err := h.registry.GetKey(ctx, [32]byte(common.HexToHash(id)))
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CHAIN_UNREACHABLE"})
			return
		}
		keys[id] = registryKeyView(key)
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
