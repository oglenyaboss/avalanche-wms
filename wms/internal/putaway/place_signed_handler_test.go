package putaway

import (
	"encoding/base64"
	"encoding/json"
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

func TestScanStorageBinHandler(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	bufferBinID := uuid.New()
	item := uuid.New()

	repo := &mockPutawayRepo{
		storageBin: &domain.Bin{BinID: storageBinID, Code: "M2-A-03"},
		productsMap: map[uuid.UUID]*domain.Product{
			item: {ProductID: item, Status: "RECEIVED", BinID: &bufferBinID},
		},
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}
	signerPasskey, _ := signedSigner()
	signerSource := &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}
	svc := putawayService(repo, ops, signerSource)

	draftBody := `{"product_ids":["` + item.String() + `"],"storage_bin_id":"` + storageBinID.String() + `"}`

	t.Run("old route is gone", func(t *testing.T) {
		rec := servePutaway(t, svc, http.MethodPost, "/putaway/scan-storage-bin", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 404 or 405", rec.Code)
		}
	})
	t.Run("draft decode error", func(t *testing.T) {
		rec := servePutaway(t, svc, http.MethodPost, "/putaway/scan-storage-bin/draft", `{`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft validates product_ids", func(t *testing.T) {
		rec := servePutaway(t, svc, http.MethodPost, "/putaway/scan-storage-bin/draft",
			`{"product_ids":[],"storage_bin_id":"`+storageBinID.String()+`"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for empty product_ids (body=%s)", rec.Code, rec.Body.String())
		}
		rec = servePutaway(t, svc, http.MethodPost, "/putaway/scan-storage-bin/draft",
			`{"product_ids":["not-a-uuid"],"storage_bin_id":"`+storageBinID.String()+`"}`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for bad product uuid (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft maps REGISTRY_UNAVAILABLE", func(t *testing.T) {
		failingSigner := &mockSignerSource{signerErr: fmt.Errorf("x: %w", passkeys.ErrRegistryUnavailable)}
		failingSvc := putawayService(repo, ops, failingSigner)
		rec := servePutaway(t, failingSvc, http.MethodPost, "/putaway/scan-storage-bin/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"REGISTRY_UNAVAILABLE"`) {
			t.Errorf("body lacks REGISTRY_UNAVAILABLE: %s", rec.Body.String())
		}
	})
	t.Run("draft returns parts", func(t *testing.T) {
		rec := servePutaway(t, svc, http.MethodPost, "/putaway/scan-storage-bin/draft", draftBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"parts"`) || !strings.Contains(rec.Body.String(), `"challenge"`) {
			t.Errorf("draft response lacks parts/challenge: %s", rec.Body.String())
		}
	})
	t.Run("submit decode error", func(t *testing.T) {
		rec := servePutaway(t, svc, http.MethodPost, "/putaway/scan-storage-bin/submit", `{`, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("submit maps OPERATION_NOT_FOUND", func(t *testing.T) {
		rec := servePutaway(t, svc, http.MethodPost, "/putaway/scan-storage-bin/submit",
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
		op, opSigner, key := newSignedOperation(t, storageBinID, operatorID, []uuid.UUID{item})
		ops.operations[op.OperationID] = op
		repo.lockedOperation = op
		opSignerSource := &mockSignerSource{signer: opSigner, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}
		submitSvc := putawayService(repo, ops, opSignerSource)

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

		// Товар больше не RECEIVED → подпись устарела.
		repo.productsMap[item].Status = "STORED"
		staleBody := buildSubmitBody(t, op.OperationID.String(), assertions)
		rec := servePutaway(t, submitSvc, http.MethodPost, "/putaway/scan-storage-bin/submit", staleBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 DRAFT_STALE (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "DRAFT_STALE") {
			t.Errorf("body lacks DRAFT_STALE: %s", rec.Body.String())
		}

		// Товар жив → submit проходит, ответ несёт operation_id.
		repo.productsMap[item].Status = "RECEIVED"
		okBody := buildSubmitBody(t, op.OperationID.String(), assertions)
		rec = servePutaway(t, submitSvc, http.MethodPost, "/putaway/scan-storage-bin/submit", okBody, operatorID, domain.UserRoleOperator)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"operation_id":"`+op.OperationID.String()+`"`) {
			t.Errorf("body lacks operation_id: %s", rec.Body.String())
		}
	})
}

// buildSubmitBody renders the submit request with one assertion per part.
func buildSubmitBody(t *testing.T, operationID string, assertions []map[string]string) string {
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

// TestScanStorageBinHandler_CargoplaceNotClosed maps the cargoplace gate to 409
// so the client can tell "not closed on receiving yet" from other conflicts.
func TestScanStorageBinHandler_CargoplaceNotClosed(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	bufferBinID := uuid.New()
	cpA := uuid.New()
	item := uuid.New()

	repo := &mockPutawayRepo{
		storageBin: &domain.Bin{BinID: storageBinID, Code: "M2-A-03"},
		productsMap: map[uuid.UUID]*domain.Product{
			item: {ProductID: item, Status: "RECEIVED", BinID: &bufferBinID, CargoplaceID: cpA},
		},
		cargoplaceStatuses: map[uuid.UUID]string{cpA: "TABLE_IN_PROGRESS"},
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}
	signerPasskey, _ := signedSigner()
	svc := putawayService(repo, ops, &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}})

	body := `{"product_ids":["` + item.String() + `"],"storage_bin_id":"` + storageBinID.String() + `"}`
	rec := servePutaway(t, svc, http.MethodPost, "/putaway/scan-storage-bin/draft", body, operatorID, domain.UserRoleOperator)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Error   *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error envelope: %v (%s)", err, rec.Body.String())
	}
	if resp.Error == nil || resp.Error.Code != "CARGOPLACE_NOT_CLOSED" {
		t.Fatalf("error.code = %+v, want CARGOPLACE_NOT_CLOSED (body=%s)", resp.Error, rec.Body.String())
	}
}
