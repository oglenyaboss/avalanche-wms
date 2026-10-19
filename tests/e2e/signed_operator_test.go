//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// signedOperator is an OPERATOR user whose passkey was confirmed on chain, so
// it can sign warehouse actions: draft → soft sign → submit.
type signedOperator struct {
	userID   uuid.UUID
	username string
	token    string
	key      *ecdsa.PrivateKey
	credID   []byte
	keyID    [32]byte
}

// consortiumOrg is the shared consortium created once per test binary: org +
// ADMIN user + admin passkey registered on chain by the owner.
type consortiumOrg struct {
	orgID       uuid.UUID
	adminUserID uuid.UUID
	adminToken  string
	adminKey    *ecdsa.PrivateKey
	adminCred   []byte
}

var (
	consortiumOnce     sync.Once
	consortiumShared   *consortiumOrg
	consortiumSetupErr error
)

// requireConsortium returns the shared consortium organization, creating it on
// the first call. The rows are intentionally never cleaned up: deleting the org
// or its admin passkey would break every later test, ids are random per run and
// the e2e stack is wiped with its volumes between runs.
func requireConsortium(t *testing.T, ctx context.Context, env *env) *consortiumOrg {
	t.Helper()
	consortiumOnce.Do(func() {
		// The sentinel is overwritten on success; if setup aborts via require
		// (Goexit), later callers still report a meaningful error.
		consortiumSetupErr = errors.New("consortium org setup did not complete")
		consortiumSetupErr = setupConsortium(t, ctx, env)
	})
	require.NoError(t, consortiumSetupErr, "shared consortium org is not available")
	require.NotNil(t, consortiumShared, "shared consortium org was not initialized")
	return consortiumShared
}

// setupConsortium inserts the org + ADMIN user + PENDING admin passkey (same
// rows as newKeyActionFixture, no cleanup), registers the org on chain by the
// owner, recreates the adapter exactly once (the owner tx consumed one nonce of
// the adapter's deployer key) and waits until the admin passkey is ACTIVE.
func setupConsortium(t *testing.T, ctx context.Context, env *env) error {
	fx := &keyActionFixture{
		suffix:    shortSuffix(),
		orgID:     uuid.New(),
		password:  "e2e-pass-123",
		adminUser: "sadmin-" + shortSuffix(),
	}
	fx.orgName = "e2e-consortium-" + fx.suffix
	fx.adminUserID = uuid.New()
	fx.adminKey = newSoftKey()
	fx.adminCred = randomCredID(t)
	fx.adminQX, fx.adminQY = softCoordinates(fx.adminKey)
	fx.adminKeyID = softKeyID(fx.adminQX, fx.adminQY)

	mustExec(t, ctx, env, `INSERT INTO public.organizations (org_id, name) VALUES ($1, $2)`, fx.orgID, fx.orgName)
	mustExec(t, ctx, env, `
		INSERT INTO public.users (user_id, username, password_hash, role, is_active, org_id)
		VALUES ($1, $2, crypt($3, gen_salt('bf')), 'ADMIN'::public.user_role, true, $4)`,
		fx.adminUserID, fx.adminUser, fx.password, fx.orgID)
	mustExec(t, ctx, env, `
		INSERT INTO public.passkey_credentials (credential_id, user_id, org_id, qx, qy, key_id, role, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'ADMIN', 'PENDING')`,
		fx.adminCred, fx.adminUserID, fx.orgID, fx.adminQX[:], fx.adminQY[:], fx.adminKeyID[:])

	owner := ownerKeyForTest(t, ctx)
	registryHex, err := readSharedStateFile(ctx, "registry_addr.txt")
	if err != nil {
		return err
	}
	registerOrgOnChain(t, ctx, env, owner, common.HexToAddress(registryHex), fx)
	recreateAdapterWithEnv(t, ctx, env, nil)

	adminToken := loginAs(t, env, fx.adminUser, fx.password)
	waitForOrganization(t, ctx, env, adminToken, 60*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, fx.adminUser)
		return ok && m.Passkey != nil && m.Passkey.Status == "ACTIVE"
	})

	consortiumShared = &consortiumOrg{
		orgID:       fx.orgID,
		adminUserID: fx.adminUserID,
		adminToken:  adminToken,
		adminKey:    fx.adminKey,
		adminCred:   fx.adminCred,
	}
	return nil
}

// newSignedOperator inserts a fresh OPERATOR user + PENDING passkey into the
// shared consortium org, has the shared admin confirm it end to end (the
// TestKeyConfirmation_EndToEnd flow) and returns the operator once its passkey
// is ACTIVE and it can log in.
//
// The operator's rows are intentionally NOT cleaned up: warehouse_actions rows
// reference the passkey credential (ON DELETE RESTRICT) and the user, and the
// domain rows a test produces (products, receiving_table logs, outbox events)
// reference them too, so no deletion order is safe for an arbitrary flow. Ids
// are random per run and the e2e stack is destroyed with its volumes between
// runs, so the leftover rows are harmless.
func newSignedOperator(t *testing.T, ctx context.Context, env *env) *signedOperator {
	t.Helper()
	shared := requireConsortium(t, ctx, env)

	op := &signedOperator{
		userID:   uuid.New(),
		username: "sop-" + shortSuffix(),
		key:      newSoftKey(),
		credID:   randomCredID(t),
	}
	qx, qy := softCoordinates(op.key)
	op.keyID = softKeyID(qx, qy)

	mustExec(t, ctx, env, `
		INSERT INTO public.users (user_id, username, password_hash, role, is_active, org_id)
		VALUES ($1, $2, crypt($3, gen_salt('bf')), 'OPERATOR'::public.user_role, true, $4)`,
		op.userID, op.username, "e2e-pass-123", shared.orgID)
	mustExec(t, ctx, env, `
		INSERT INTO public.passkey_credentials (credential_id, user_id, org_id, qx, qy, key_id, role, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'OPERATOR', 'PENDING')`,
		op.credID, op.userID, shared.orgID, qx[:], qy[:], op.keyID[:])

	var draft keyActionDraftDTO
	postJSON(t, env, shared.adminToken, "/passkeys/actions/key-register/draft", map[string]string{
		"target_key_id": "0x" + hex.EncodeToString(op.keyID[:]),
	}, &draft)
	actionID, err := uuid.Parse(draft.ActionID)
	require.NoError(t, err)
	challengeBytes, err := base64.RawURLEncoding.DecodeString(draft.Challenge)
	require.NoError(t, err)
	var challenge [32]byte
	copy(challenge[:], challengeBytes)
	assertion := softSign(shared.adminKey, challenge)

	var sent confirmationDTO
	postJSON(t, env, shared.adminToken, "/passkeys/actions/"+draft.ActionID+"/submit", map[string]string{
		"credential_id":      base64.RawURLEncoding.EncodeToString(shared.adminCred),
		"authenticator_data": base64.RawURLEncoding.EncodeToString(assertion.AuthenticatorData),
		"client_data_json":   base64.RawURLEncoding.EncodeToString(assertion.ClientDataJSON),
		"signature":          base64.RawURLEncoding.EncodeToString(assertion.Signature),
	}, &sent)
	require.Equal(t, "SENT", sent.State)

	waitForOnchainKeyAction(t, ctx, env, actionID, "COMMITTED", nil)
	waitForOrganization(t, ctx, env, shared.adminToken, 30*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, op.username)
		return ok && m.Passkey != nil && m.Passkey.Status == "ACTIVE"
	})

	op.token = loginAs(t, env, op.username, "e2e-pass-123")
	return op
}

// ── signed cargoplace close DTOs and helpers ─────────────────────────────────

// closeDraftDTO models the draft response of the signed cargoplace close.
type closeDraftDTO struct {
	OperationID      string `json:"operation_id"`
	ActionType       int    `json:"action_type"`
	SignerKeyID      string `json:"signer_key_id"`
	RPID             string `json:"rp_id"`
	AllowCredentials []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"allow_credentials"`
	UserVerification string              `json:"user_verification"`
	Timeout          int                 `json:"timeout"`
	Parts            []closeDraftPartDTO `json:"parts"`
}

type closeDraftPartDTO struct {
	ActionID    string   `json:"action_id"`
	ItemIDs     []string `json:"item_ids"`
	DetailsHash string   `json:"details_hash"`
	Challenge   string   `json:"challenge"`
}

// closeCargoplaceResultDTO models the submit response of the signed close.
type closeCargoplaceResultDTO struct {
	CargoplaceID        string `json:"cargoplace_id"`
	OperationID         string `json:"operation_id"`
	Status              string `json:"status"`
	OutboxEventsCreated int    `json:"outbox_events_created"`
	Summary             struct {
		ProductsReceived int `json:"products_received"`
		ProductsExpected int `json:"products_expected"`
		Shortage         int `json:"shortage"`
	} `json:"summary"`
}

// draftCloseCargopliceDraft POSTs the draft endpoint.
func draftCloseCargoplace(t *testing.T, env *env, op *signedOperator, cargoplaceID string) closeDraftDTO {
	t.Helper()
	var draft closeDraftDTO
	postJSON(t, env, op.token, "/receiving/table/close-cargoplace/draft", map[string]string{
		"cargoplace_id": cargoplaceID,
	}, &draft)
	return draft
}

// signDraft soft-signs every part's challenge (decoded from base64url) in part
// order and returns the assertion objects for the submit body.
func signDraft(t *testing.T, op *signedOperator, draft closeDraftDTO) []map[string]string {
	t.Helper()
	out := make([]map[string]string, 0, len(draft.Parts))
	for _, p := range draft.Parts {
		challengeBytes, err := base64.RawURLEncoding.DecodeString(p.Challenge)
		require.NoError(t, err)
		var challenge [32]byte
		copy(challenge[:], challengeBytes)
		a := softSign(op.key, challenge)
		out = append(out, map[string]string{
			"action_id":          p.ActionID,
			"credential_id":      base64.RawURLEncoding.EncodeToString(op.credID),
			"authenticator_data": base64.RawURLEncoding.EncodeToString(a.AuthenticatorData),
			"client_data_json":   base64.RawURLEncoding.EncodeToString(a.ClientDataJSON),
			"signature":          base64.RawURLEncoding.EncodeToString(a.Signature),
		})
	}
	return out
}

// submitCloseCargoplace POSTs the submit endpoint and requires 200.
func submitCloseCargoplace(t *testing.T, env *env, token, operationID string, assertions []map[string]string) closeCargoplaceResultDTO {
	t.Helper()
	var result closeCargoplaceResultDTO
	postJSON(t, env, token, "/receiving/table/close-cargoplace/submit", map[string]any{
		"operation_id": operationID,
		"assertions":   assertions,
	}, &result)
	return result
}

// closeCargoplaceSigned runs the whole signed close: draft → sign → submit.
func closeCargoplaceSigned(t *testing.T, env *env, op *signedOperator, cargoplaceID string) closeCargoplaceResultDTO {
	t.Helper()
	draft := draftCloseCargoplace(t, env, op, cargoplaceID)
	assertions := signDraft(t, op, draft)
	return submitCloseCargoplace(t, env, op.token, draft.OperationID, assertions)
}

// operationStatusDTO models GET /signed-actions/operations/{id}.
type operationStatusDTO struct {
	OperationID string `json:"operation_id"`
	Parts       []struct {
		ActionID     string `json:"action_id"`
		State        string `json:"state"`
		RejectReason *int16 `json:"reject_reason"`
	} `json:"parts"`
}

// fetchOperationStatus GETs the operation status without failing the test on
// transient errors, so it can be used inside a poll loop.
func fetchOperationStatus(ctx context.Context, env *env, token, operationID string) (operationStatusDTO, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.wmsURL+"/signed-actions/operations/"+operationID, nil)
	if err != nil {
		return operationStatusDTO{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.httpClient.Do(req)
	if err != nil {
		return operationStatusDTO{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return operationStatusDTO{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return operationStatusDTO{}, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var envelope struct {
		Success bool               `json:"success"`
		Data    operationStatusDTO `json:"data"`
		Error   *apiError          `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return operationStatusDTO{}, err
	}
	if !envelope.Success || envelope.Error != nil {
		return operationStatusDTO{}, fmt.Errorf("api error in body: %s", body)
	}
	return envelope.Data, nil
}

// waitForOperationState polls the operation status until every part is in
// wantState (with wantReason, nil = must be NULL) and returns the last view.
func waitForOperationState(t *testing.T, ctx context.Context, env *env, token, operationID, wantState string, wantReason *int16) operationStatusDTO {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last operationStatusDTO
	for {
		if status, err := fetchOperationStatus(ctx, env, token, operationID); err == nil {
			last = status
			ok := len(status.Parts) > 0
			for _, p := range status.Parts {
				if p.State != wantState {
					ok = false
					break
				}
				if wantReason == nil && p.RejectReason != nil {
					ok = false
					break
				}
				if wantReason != nil && (p.RejectReason == nil || *p.RejectReason != *wantReason) {
					ok = false
					break
				}
			}
			if ok {
				return status
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			raw, _ := json.Marshal(last)
			t.Fatalf("operation %s did not reach %s in 90s: %s", operationID, wantState, raw)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("operation poll interrupted: %v", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// ── independent keccak re-implementations of ADR-0001 hashing ─────────────────

// softReceiveDetailsHash is an independent re-implementation of ADR-0001 §4 for
// Receive: keccak256(abi.encode(bytes32 salt, bytes16 cargoplaceId,
// bytes16 operatorId, uint64 occurredAtMs)). bytes16 words are left-aligned
// (zero tail), the uint64 is right-aligned big-endian in its word.
func softReceiveDetailsHash(salt [32]byte, cargoplaceID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	var buf [128]byte
	copy(buf[0:32], salt[:])
	copy(buf[32:48], cargoplaceID[:])
	copy(buf[64:80], operatorID[:])
	binary.BigEndian.PutUint64(buf[120:128], occurredAtMs)
	var out [32]byte
	copy(out[:], crypto.Keccak256(buf[:]))
	return out
}

// softItemsHash is keccak256 of the concatenated 32-byte item ids in the given
// order (ADR-0001 §3); empty input equals keccak256("").
func softItemsHash(itemIDs []uuid.UUID) [32]byte {
	buf := make([]byte, 0, 32*len(itemIDs))
	for _, id := range itemIDs {
		buf = append(buf, id[:]...)
	}
	var out [32]byte
	copy(out[:], crypto.Keccak256(buf))
	return out
}

// warehouseActionsContainingProduct counts submitted and draft signed actions
// whose item_ids contain the product.
func warehouseActionsContainingProduct(t *testing.T, ctx context.Context, env *env, productID uuid.UUID) int {
	t.Helper()
	var count int
	err := env.db.QueryRow(ctx, `
		SELECT count(*) FROM public.warehouse_actions
		WHERE item_ids @> ARRAY[$1::uuid]`, productID).Scan(&count)
	require.NoError(t, err)
	return count
}

// bigIntFromWord reads a 32-byte ABI word as a big-endian uint256.
func bigIntFromWord(word []byte) *big.Int {
	return new(big.Int).SetBytes(word)
}
