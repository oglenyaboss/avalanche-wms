package receiving

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/passkeys"
	"wms/internal/signedaction/signedactiontest"
	"wms/internal/signedops"
)

func TestCloseCargoplaceHandler(t *testing.T) {
	cargoplaceID := uuid.New()
	operatorID := uuid.New()
	item := uuid.New()

	repo := &mockReceivingRepo{
		cargoplaceByID:     &domain.Cargoplace{CargoplaceID: cargoplaceID, Status: cargoplaceStatusTableInProgress},
		closableProductIDs: []uuid.UUID{item},
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}
	signerPasskey, _ := signedSigner()
	signerSource := &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}
	svc := closeService(repo, ops, signerSource)

	t.Run("old route is gone", func(t *testing.T) {
		rec := serveClose(t, svc, http.MethodPost, "/receiving/table/close-cargoplace", `{"cargoplace_id":"`+cargoplaceID.String()+`"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 404 or 405", rec.Code)
		}
	})
	t.Run("draft decode error", func(t *testing.T) {
		rec := serveClose(t, svc, http.MethodPost, "/receiving/table/close-cargoplace/draft", `{`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("submit decode error", func(t *testing.T) {
		rec := serveClose(t, svc, http.MethodPost, "/receiving/table/close-cargoplace/submit", `{`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("empty cargoplace is 409 CARGOPLACE_EMPTY", func(t *testing.T) {
		emptyRepo := &mockReceivingRepo{
			cargoplaceByID:     &domain.Cargoplace{CargoplaceID: cargoplaceID, Status: cargoplaceStatusTableInProgress},
			closableProductIDs: []uuid.UUID{},
		}
		emptySvc := closeService(emptyRepo, ops, signerSource)
		rec := serveClose(t, emptySvc, http.MethodPost, "/receiving/table/close-cargoplace/draft",
			`{"cargoplace_id":"`+cargoplaceID.String()+`"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"CARGOPLACE_EMPTY"`) {
			t.Errorf("body lacks CARGOPLACE_EMPTY: %s", rec.Body.String())
		}
	})
	t.Run("draft maps REGISTRY_UNAVAILABLE", func(t *testing.T) {
		failingSigner := &mockSignerSource{signerErr: fmt.Errorf("x: %w", passkeys.ErrRegistryUnavailable)}
		failingSvc := closeService(repo, ops, failingSigner)
		rec := serveClose(t, failingSvc, http.MethodPost, "/receiving/table/close-cargoplace/draft",
			`{"cargoplace_id":"`+cargoplaceID.String()+`"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"REGISTRY_UNAVAILABLE"`) {
			t.Errorf("body lacks REGISTRY_UNAVAILABLE: %s", rec.Body.String())
		}
	})
	t.Run("draft returns parts", func(t *testing.T) {
		rec := serveClose(t, svc, http.MethodPost, "/receiving/table/close-cargoplace/draft",
			`{"cargoplace_id":"`+cargoplaceID.String()+`"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"parts"`) || !strings.Contains(rec.Body.String(), `"challenge"`) {
			t.Errorf("draft response lacks parts/challenge: %s", rec.Body.String())
		}
	})
}
