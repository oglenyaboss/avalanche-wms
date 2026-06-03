// Package onchain exposes a read-only proxy to the ledger-adapter's live
// transaction-receipt endpoint, so the browser can verify a tx against the chain
// without reaching the adapter or RPC directly.
package onchain

import (
	"context"
	"log"
	"net/http"
	"regexp"

	"github.com/gorilla/mux"

	"wms/internal/ledger"
	"wms/internal/platform/httputil"
)

// ReceiptGetter is the slice of *ledger.Client this handler needs (test seam).
// Exported so main.go can build an explicit nil interface when the adapter URL
// is unset — a typed-nil *ledger.Client stored directly would defeat the nil
// check below (interface holding a nil pointer is NOT == nil).
type ReceiptGetter interface {
	GetTxReceipt(ctx context.Context, txHash string) (ledger.TxReceipt, error)
}

type Handler struct {
	ledger ReceiptGetter
}

func NewHandler(l ReceiptGetter) *Handler { return &Handler{ledger: l} }

func (h *Handler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/tx/{hash}", h.GetTx).Methods(http.MethodGet)
	router.HandleFunc("/tx/{hash}/", h.GetTx).Methods(http.MethodGet)
}

var txHashRe = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

// GetTx proxies the adapter receipt. found:false (not yet mined) is a 200 success;
// any adapter/transport failure becomes 502 CHAIN_UNREACHABLE (adapter JSON is
// never leaked to the browser).
func (h *Handler) GetTx(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireAdminOrOperator(w, r); !ok {
		return
	}
	hash := mux.Vars(r)["hash"]
	if !txHashRe.MatchString(hash) {
		httputil.WriteError(w, http.StatusBadRequest, "INVALID_TX_HASH", "Некорректный хеш транзакции")
		return
	}
	if h.ledger == nil {
		httputil.WriteError(w, http.StatusBadGateway, "CHAIN_UNREACHABLE", "Сеть недоступна")
		return
	}
	rec, err := h.ledger.GetTxReceipt(r.Context(), hash)
	if err != nil {
		log.Printf("onchain: GetTx %s -> 502: %v", hash, err)
		httputil.WriteError(w, http.StatusBadGateway, "CHAIN_UNREACHABLE", "Сеть недоступна, попробуйте ещё раз")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.Envelope{Success: true, Data: rec, Error: nil})
}
