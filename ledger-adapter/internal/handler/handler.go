package handler

import (
	"encoding/json"
	"net/http"
	"time"
)

// Handler holds HTTP handler state. Сейчас stateless — оставлен как struct для
// совместимости и будущих расширений (readiness depends на Pool/Consumer).
type Handler struct{}

// New creates a new Handler.
func New() *Handler { return &Handler{} }

// RegisterRoutes регистрирует только /health — вся business logic ушла в
// consumer/store/chain, HTTP surface минимальный для docker healthcheck.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.Health)
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
