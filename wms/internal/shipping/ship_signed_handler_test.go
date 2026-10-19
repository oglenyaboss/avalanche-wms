package shipping

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
	"wms/internal/signedaction/signedactiontest"
)

func serveShipping(t *testing.T, svc *Service, method, path, body string, userID uuid.UUID, role domain.UserRole) *httptest.ResponseRecorder {
	t.Helper()
	r := mux.NewRouter()
	NewHandler(svc).RegisterRoutes(r.PathPrefix("/shipping").Subrouter())
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), userID, role))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestShipSignedHandler(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	destinationID := uuid.New()
	item := uuid.New()
	orderID := uuid.New()

	newEnv := func() (*mockShippingRepo, *shipMockOps, *Service) {
		repo := &mockShippingRepo{
			bin:          bufferBinRecord(bufferBinID, destinationID),
			dispatch:     atGateDispatch(dispatchID, destinationID),
			listProducts: []ProductForShip{{ProductID: item, OrderID: &orderID}},
		}
		ops := newShipMockOps()
		signerPasskey, _ := shipSigner()
		svc := NewService(repo, ops, &shipMockSigner{signer: signerPasskey})
		return repo, ops, svc
	}

	draftBody := `{"buffer_bin_id":"` + bufferBinID.String() + `","dispatch_id":"` + dispatchID.String() + `"}`

	t.Run("unsigned route is gone", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 404 or 405", rec.Code)
		}
	})
	t.Run("draft decode error", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/draft", `{`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft validates uuids", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/draft", `{"buffer_bin_id":"nope","dispatch_id":"`+dispatchID.String()+`"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for bad buffer_bin_id (body=%s)", rec.Code, rec.Body.String())
		}
		rec = serveShipping(t, svc, http.MethodPost, "/shipping/ship/draft", `{"buffer_bin_id":"`+bufferBinID.String()+`","dispatch_id":"nope"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for bad dispatch_id (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft caps spot product_ids at 200", func(t *testing.T) {
		_, _, svc := newEnv()
		ids := make([]string, 201)
		for i := range ids {
			ids[i] = `"` + uuid.New().String() + `"`
		}
		body := `{"buffer_bin_id":"` + bufferBinID.String() + `","dispatch_id":"` + dispatchID.String() + `","product_ids":[` + strings.Join(ids, ",") + `]}`
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/draft", body, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "product_ids") {
			t.Fatalf("status = %d, want 400 about product_ids (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft maps BIN_NOT_SHIPPING_BUFFER", func(t *testing.T) {
		repo, _, svc := newEnv()
		section := "A"
		repo.bin = &BufferBinRecord{BinID: bufferBinID, Section: &section}
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "BIN_NOT_SHIPPING_BUFFER") {
			t.Fatalf("status = %d, want 400 BIN_NOT_SHIPPING_BUFFER (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft maps DISPATCH_NOT_AT_GATE", func(t *testing.T) {
		repo, _, svc := newEnv()
		repo.dispatch.Status = domain.OutboundDispatchStatusScheduled
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "DISPATCH_NOT_AT_GATE") {
			t.Fatalf("status = %d, want 409 DISPATCH_NOT_AT_GATE (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft maps PICKING_NOT_CONFIRMED with the count in the message", func(t *testing.T) {
		repo, _, svc := newEnv()
		repo.unconfirmedProducts = []uuid.UUID{uuid.New(), uuid.New()}
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "PICKING_NOT_CONFIRMED") {
			t.Fatalf("status = %d, want 409 PICKING_NOT_CONFIRMED (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "2 шт.") {
			t.Errorf("message lacks the unconfirmed count: %s", rec.Body.String())
		}
	})
	t.Run("draft returns parts", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"parts"`) || !strings.Contains(rec.Body.String(), `"challenge"`) {
			t.Errorf("draft response lacks parts/challenge: %s", rec.Body.String())
		}
	})
	t.Run("submit decode error", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/submit", `{`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("submit maps OPERATION_NOT_FOUND", func(t *testing.T) {
		_, _, svc := newEnv()
		rec := serveShipping(t, svc, http.MethodPost, "/shipping/ship/submit",
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
		op, opSigner, key := newShipOperation(t, dispatchID, bufferBinID, operatorID, map[uuid.UUID]uuid.UUID{item: orderID}, []uuid.UUID{item})
		ops.operations[op.OperationID] = op
		repo.lockedOperation = op
		repo.lockedProducts = []ProductForShip{{ProductID: item, OrderID: &orderID}}
		opSignerSource := &shipMockSigner{signer: opSigner}
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

		// Товар уже не READY_TO_SHIP в этой ячейке → подпись устарела.
		repo.lockedProducts = nil
		staleBody := buildShipSubmitBody(t, op.OperationID.String(), assertions)
		rec := serveShipping(t, submitSvc, http.MethodPost, "/shipping/ship/submit", staleBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 DRAFT_STALE (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "DRAFT_STALE") {
			t.Errorf("body lacks DRAFT_STALE: %s", rec.Body.String())
		}

		// Товар на месте → submit проходит, ответ несёт operation_id.
		repo.lockedProducts = []ProductForShip{{ProductID: item, OrderID: &orderID}}
		okBody := buildShipSubmitBody(t, op.OperationID.String(), assertions)
		rec = serveShipping(t, submitSvc, http.MethodPost, "/shipping/ship/submit", okBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"operation_id":"`+op.OperationID.String()+`"`) {
			t.Errorf("body lacks operation_id: %s", rec.Body.String())
		}
	})
}

// buildShipSubmitBody renders the submit request with one assertion per part.
func buildShipSubmitBody(t *testing.T, operationID string, assertions []map[string]string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"operation_id": operationID,
		"assertions":   assertions,
	})
	if err != nil {
		t.Fatalf("marshal submit body: %v", err)
	}
	return string(body)
}
