package assembly

import (
	"encoding/base64"
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

func TestUnloadSignedHandler(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	destinationID := uuid.New()
	item := uuid.New()
	orderID := uuid.New()

	newEnv := func() (*mockAssemblyRepo, *mockOperationStore, *Service) {
		repo := &mockAssemblyRepo{
			shippingBufferBin: shippingBufferBin(bufferBinID, destinationID),
			basket:            []BasketItem{{ProductID: item, OrderID: &orderID}},
			lockedBasket:      []BasketItem{{ProductID: item, OrderID: &orderID}},
		}
		ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}
		signerPasskey, _ := signedSigner()
		svc := NewService(repo, ops, &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}})
		return repo, ops, svc
	}

	draftBody := `{"buffer_bin_id":"` + bufferBinID.String() + `"}`

	t.Run("old route is gone", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 404 or 405", rec.Code)
		}
	})
	t.Run("draft decode error", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer/draft", `{`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft validates buffer_bin_id", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer/draft", `{"buffer_bin_id":"not-a-uuid"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for bad uuid (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft maps BIN_NOT_SHIPPING_BUFFER", func(t *testing.T) {
		repo, _, svc := newEnv()
		repo.shippingBufferBinErr = ErrBinNotShippingBuffer
		rec := serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "BIN_NOT_SHIPPING_BUFFER") {
			t.Fatalf("status = %d, want 400 BIN_NOT_SHIPPING_BUFFER (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft maps CART_EMPTY and DESTINATION_MISMATCH", func(t *testing.T) {
		repo, _, svc := newEnv()
		repo.basket = nil
		rec := serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "CART_EMPTY") {
			t.Fatalf("status = %d, want 409 CART_EMPTY (body=%s)", rec.Code, rec.Body.String())
		}
		repo.assembledProducts = []uuid.UUID{uuid.New()}
		rec = serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "DESTINATION_MISMATCH") {
			t.Fatalf("status = %d, want 409 DESTINATION_MISMATCH (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft maps REGISTRY_UNAVAILABLE", func(t *testing.T) {
		repo, ops, _ := newEnv()
		failingSvc := NewService(repo, ops, &mockSignerSource{signerErr: fmt.Errorf("x: %w", passkeys.ErrRegistryUnavailable)})
		rec := serveAssembly(t, failingSvc, http.MethodPost, "/assembly/scan-shipping-buffer/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"REGISTRY_UNAVAILABLE"`) {
			t.Errorf("body lacks REGISTRY_UNAVAILABLE: %s", rec.Body.String())
		}
	})
	t.Run("draft returns parts", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"parts"`) || !strings.Contains(rec.Body.String(), `"challenge"`) {
			t.Errorf("draft response lacks parts/challenge: %s", rec.Body.String())
		}
	})
	t.Run("submit decode error", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer/submit", `{`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("submit maps OPERATION_NOT_FOUND", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveAssembly(t, svc, http.MethodPost, "/assembly/scan-shipping-buffer/submit",
			`{"operation_id":"`+uuid.New().String()+`","assertions":[]}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "OPERATION_NOT_FOUND") {
			t.Errorf("body lacks OPERATION_NOT_FOUND: %s", rec.Body.String())
		}
	})
	t.Run("submit maps DRAFT_STALE and carries operation_id on success", func(t *testing.T) {
		// Операция подписана своим ключом: ActiveSigner сервиса должен вернуть
		// именно её подписанта, иначе подпись не сойдётся.
		repo, ops, _ := newEnv()
		op, opSigner, key := newPickOperation(t, bufferBinID, operatorID, map[uuid.UUID]uuid.UUID{item: orderID}, []uuid.UUID{item})
		ops.operations[op.OperationID] = op
		repo.lockedOperation = op
		opSignerSource := &mockSignerSource{signer: opSigner, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}
		submitSvc := NewService(repo, ops, opSignerSource)

		assertions := make([]map[string]string, 0, len(op.Parts))
		for _, p := range op.Parts {
			a := signedactiontest.Sign(key, p.Challenge)
			a.CredentialID = opSigner.CredentialID
			assertions = append(assertions, map[string]string{
				"action_id":          p.ActionID.String(),
				"credential_id":      base64.RawURLEncoding.EncodeToString(a.CredentialID),
				"authenticator_data": base64.RawURLEncoding.EncodeToString(a.AuthenticatorData),
				"client_data_json":   base64.RawURLEncoding.EncodeToString(a.ClientDataJSON),
				"signature":          base64.RawURLEncoding.EncodeToString(a.Signature),
			})
		}

		// Корзина осиротела → подпись устарела.
		repo.lockedBasket = nil
		staleBody := buildSubmitBody(t, op.OperationID.String(), assertions)
		rec := serveAssembly(t, submitSvc, http.MethodPost, "/assembly/scan-shipping-buffer/submit", staleBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 DRAFT_STALE (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "DRAFT_STALE") {
			t.Errorf("body lacks DRAFT_STALE: %s", rec.Body.String())
		}

		// Корзина на месте → submit проходит, ответ несёт operation_id.
		repo.lockedBasket = []BasketItem{{ProductID: item, OrderID: &orderID}}
		okBody := buildSubmitBody(t, op.OperationID.String(), assertions)
		rec = serveAssembly(t, submitSvc, http.MethodPost, "/assembly/scan-shipping-buffer/submit", okBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"operation_id":"`+op.OperationID.String()+`"`) {
			t.Errorf("body lacks operation_id: %s", rec.Body.String())
		}
	})
}
