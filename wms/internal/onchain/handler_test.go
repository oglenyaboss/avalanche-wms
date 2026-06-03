package onchain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
	"wms/internal/ledger"
)

type stubGetter struct {
	rec ledger.TxReceipt
	err error
}

func (s stubGetter) GetTxReceipt(_ context.Context, _ string) (ledger.TxReceipt, error) {
	return s.rec, s.err
}

// asOperator injects an authenticated identity so RequireAdminOrOperator (top of
// the handler) passes. Mirrors wms/internal/destinations/handler_test.go.
func asOperator(req *http.Request) *http.Request {
	ctx := auth.ContextWithIdentity(req.Context(), uuid.New(), domain.UserRoleOperator)
	return req.WithContext(ctx)
}

func serve(h *Handler, path string) *httptest.ResponseRecorder {
	r := mux.NewRouter()
	sub := r.PathPrefix("/onchain").Subrouter()
	h.RegisterRoutes(sub)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, asOperator(httptest.NewRequest(http.MethodGet, path, nil)))
	return rec
}

func TestGetTx_InvalidHash400(t *testing.T) {
	rec := serve(NewHandler(stubGetter{}), "/onchain/tx/0xnothex")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGetTx_AdapterErrorIs502(t *testing.T) {
	rec := serve(NewHandler(stubGetter{err: errors.New("boom")}), "/onchain/tx/0x"+strings.Repeat("a", 64))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestGetTx_Success200(t *testing.T) {
	rec := serve(
		NewHandler(stubGetter{rec: ledger.TxReceipt{Found: true, Status: "success", BlockNumber: 42}}),
		"/onchain/tx/0x"+strings.Repeat("a", 64),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
