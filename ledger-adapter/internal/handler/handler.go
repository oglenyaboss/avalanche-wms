package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

// ChainReader is the minimal chain surface the proof endpoint needs.
// *chain.Client satisfies it. Defined here (consumer side) so the handler is
// unit-testable with a stub.
type ChainReader interface {
	TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
	BlockNumber(ctx context.Context) (uint64, error)
}

// RegistryReader is the ConsortiumRegistry read surface for /registry/keys.
// *chain.Registry satisfies it.
type RegistryReader interface {
	GetKey(ctx context.Context, keyID [32]byte) (chain.RegistryKey, error)
}

// Handler holds HTTP handler state: /health (liveness), /onchain/tx (proof)
// and /registry/keys (key status).
type Handler struct {
	chain    ChainReader
	registry RegistryReader
}

// New creates a Handler. chain or registry may be nil if the process runs without
// them (then the corresponding endpoints return 502); /health never depends on them.
func New(chain ChainReader, registry RegistryReader) *Handler {
	return &Handler{chain: chain, registry: registry}
}

// RegisterRoutes wires /health (liveness), the on-demand receipt proof and the registry key status.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.Health)
	mux.HandleFunc("GET /onchain/tx/{hash}", h.TxProof)
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

var (
	txHashRe = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
	keyIDRe  = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

type txProofResponse struct {
	Found         bool   `json:"found"`
	Status        string `json:"status"` // success | failed | pending
	BlockNumber   uint64 `json:"block_number,omitempty"`
	Confirmations uint64 `json:"confirmations,omitempty"`
	GasUsed       uint64 `json:"gas_used,omitempty"`
}

// TxProof does a single live receipt lookup (no polling) for an on-demand proof.
//
// SECURITY: this endpoint is intentionally UNAUTHENTICATED — the adapter has no
// auth layer. It is safe only because the adapter port is container-internal
// (private Docker network); the public-facing wms /onchain/tx/{hash} proxy is the
// authenticated entry point (RequireAdminOrOperator). Do NOT expose this port
// beyond the internal network without adding a shared-secret check.
func (h *Handler) TxProof(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !txHashRe.MatchString(hash) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "INVALID_TX_HASH"})
		return
	}
	if h.chain == nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CHAIN_UNREACHABLE"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	receipt, err := h.chain.TransactionReceipt(ctx, common.HexToHash(hash))
	if errors.Is(err, ethereum.NotFound) {
		writeJSON(w, http.StatusOK, txProofResponse{Found: false, Status: "pending"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CHAIN_UNREACHABLE"})
		return
	}

	resp := txProofResponse{
		Found:       true,
		Status:      "failed",
		BlockNumber: receipt.BlockNumber.Uint64(),
		GasUsed:     receipt.GasUsed,
	}
	if receipt.Status == types.ReceiptStatusSuccessful {
		resp.Status = "success"
	}
	// Confirmations clamped: a lagging head (multi-node RPC) must not underflow.
	if head, herr := h.chain.BlockNumber(ctx); herr == nil {
		if rb := receipt.BlockNumber.Uint64(); head > rb {
			resp.Confirmations = head - rb
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type registryKeyResponse struct {
	Found  bool   `json:"found"`
	OrgID  string `json:"org_id"`
	Role   string `json:"role"`
	Active bool   `json:"active"`
}

// RegistryKey — статус ключа в ConsortiumRegistry; без аутентификации, как TxProof (только внутренняя сеть).
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
	if key.Role == 0 {
		writeJSON(w, http.StatusOK, map[string]bool{"found": false})
		return
	}
	role := "operator"
	if key.Role == 1 {
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
