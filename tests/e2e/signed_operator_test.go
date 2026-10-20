//go:build e2e

package e2e

import (
	"bytes"
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
	"slices"
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
	orgID         uuid.UUID
	adminUserID   uuid.UUID
	adminUser     string
	adminPassword string
	adminToken    string
	adminKey      *ecdsa.PrivateKey
	adminCred     []byte
}

var (
	consortiumOnce     sync.Once
	consortiumShared   *consortiumOrg
	consortiumSetupErr error
	// consortiumSetupActive is true while the one-time consortium setup is
	// running. The setup itself recreates the adapter, but at that point no
	// WMS-registered key is ACTIVE yet (so no valid signed action exists) and
	// requireConsortium would re-enter the setup Once — waitAdapterConsuming
	// skips its warmup then; the key-register action the setup submits right
	// after is the first message the adapter must consume. Read/written only
	// from sequential test goroutines (no t.Parallel in this suite).
	consortiumSetupActive bool
)

// requireConsortium returns the shared consortium organization, creating it on
// the first call. The rows are intentionally never cleaned up: deleting the org
// or its admin passkey would break every later test, ids are random per run and
// the e2e stack is wiped with its volumes between runs.
func requireConsortium(t *testing.T, ctx context.Context, env *env) *consortiumOrg {
	t.Helper()
	consortiumOnce.Do(func() {
		consortiumSetupActive = true
		defer func() { consortiumSetupActive = false }()
		// The sentinel is overwritten on success; if setup aborts via require
		// (Goexit), later callers still report a meaningful error.
		consortiumSetupErr = errors.New("consortium org setup did not complete")
		consortiumSetupErr = setupConsortium(t, ctx, env)
	})
	require.NoError(t, consortiumSetupErr, "shared consortium org is not available")
	require.NotNil(t, consortiumShared, "shared consortium org was not initialized")
	// Fresh token per call: the shared one would expire (JWT_ACCESS_TTL) on
	// suites longer than the TTL, and every later signed-action draft/submit
	// would 401. The org itself is still created exactly once (sync.Once).
	shared := *consortiumShared
	shared.adminToken = loginAs(t, env, consortiumShared.adminUser, consortiumShared.adminPassword)
	return &shared
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

	adminToken := loginAs(t, env, fx.adminUser, fx.password)
	waitForOrganization(t, ctx, env, adminToken, 60*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, fx.adminUser)
		return ok && m.Passkey != nil && m.Passkey.Status == "ACTIVE"
	})

	consortiumShared = &consortiumOrg{
		orgID:         fx.orgID,
		adminUserID:   fx.adminUserID,
		adminUser:     fx.adminUser,
		adminPassword: fx.password,
		adminToken:    adminToken,
		adminKey:      fx.adminKey,
		adminCred:     fx.adminCred,
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

// ── signed putaway (basket → storage bin) DTOs and helpers ─────────────────

// draftPlaceToStorageBin POSTs the putaway draft endpoint: the draft response
// has the same shape as the cargoplace close draft, so it reuses closeDraftDTO.
func draftPlaceToStorageBin(t *testing.T, env *env, op *signedOperator, productIDs []uuid.UUID, binID uuid.UUID) closeDraftDTO {
	t.Helper()
	var draft closeDraftDTO
	postJSON(t, env, op.token, "/putaway/scan-storage-bin/draft", map[string]any{
		"product_ids":    uuidStrings(productIDs),
		"storage_bin_id": binID.String(),
	}, &draft)
	return draft
}

// submitPlaceToStorageBin POSTs the putaway submit endpoint and requires 200.
func submitPlaceToStorageBin(t *testing.T, env *env, token, operationID string, assertions []map[string]string) scanStorageBinData {
	t.Helper()
	var result scanStorageBinData
	postJSON(t, env, token, "/putaway/scan-storage-bin/submit", map[string]any{
		"operation_id": operationID,
		"assertions":   assertions,
	}, &result)
	return result
}

// placeToStorageBinSigned runs the whole signed putaway: draft → sign → submit.
func placeToStorageBinSigned(t *testing.T, env *env, op *signedOperator, productIDs []uuid.UUID, binID uuid.UUID) scanStorageBinData {
	t.Helper()
	draft := draftPlaceToStorageBin(t, env, op, productIDs, binID)
	assertions := signDraft(t, op, draft)
	return submitPlaceToStorageBin(t, env, op.token, draft.OperationID, assertions)
}

// draftPlaceToStorageBinRaw POSTs the putaway draft endpoint and returns the raw
// status + error code + message without failing, for guard tests. Product ids stay
// strings so a guard can pass a bogus (but well-formed) uuid.
func draftPlaceToStorageBinRaw(t *testing.T, env *env, token string, productIDs []string, binID string) (int, string, string) {
	t.Helper()
	return postExpectError(t, env, token, "/putaway/scan-storage-bin/draft", map[string]any{
		"product_ids":    productIDs,
		"storage_bin_id": binID,
	})
}

// submitPlaceToStorageBinRaw POSTs the putaway submit endpoint and returns the
// raw status + error code + message without failing, for guard tests.
func submitPlaceToStorageBinRaw(t *testing.T, env *env, token, operationID string, assertions []map[string]string) (int, string, string) {
	t.Helper()
	return postExpectError(t, env, token, "/putaway/scan-storage-bin/submit", map[string]any{
		"operation_id": operationID,
		"assertions":   assertions,
	})
}

// uuidStrings renders uuids as their canonical strings.
func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
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
// bytes16 operatorId, uint64 occurredAtMs)).
func softReceiveDetailsHash(salt [32]byte, cargoplaceID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	var cargo [32]byte
	copy(cargo[:16], cargoplaceID[:])
	return softEntityDetailsHash(salt, cargo, operatorID, occurredAtMs)
}

// softPutAwayDetailsHash is an independent re-implementation of ADR-0001 §4 for
// PutAway: keccak256(abi.encode(bytes32 salt, bytes16 storageBinId,
// bytes16 operatorId, uint64 occurredAtMs)).
func softPutAwayDetailsHash(salt [32]byte, storageBinID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	var bin [32]byte
	copy(bin[:16], storageBinID[:])
	return softEntityDetailsHash(salt, bin, operatorID, occurredAtMs)
}

// softDetailsWords is the one shared encoder of the ADR-0001 details-hash words
// (receive, putaway, pick): a uuid.UUID is left-aligned in its 32-byte word
// (zero tail), a uint64 is right-aligned big-endian, a [32]byte passes through
// unchanged (salt), an int encodes an ABI offset/length word.
func softDetailsWords(words ...any) []byte {
	buf := make([]byte, 0, 32*len(words))
	for _, w := range words {
		var word [32]byte
		switch v := w.(type) {
		case [32]byte:
			word = v
		case uuid.UUID:
			copy(word[:16], v[:])
		case uint64:
			binary.BigEndian.PutUint64(word[24:], v)
		case int:
			binary.BigEndian.PutUint64(word[24:], uint64(v))
		default:
			panic(fmt.Sprintf("softDetailsWords: unsupported word type %T", w))
		}
		buf = append(buf, word[:]...)
	}
	return buf
}

// softEntityDetailsHash is the shared body of the single-entity details hashes
// (receive, putaway).
func softEntityDetailsHash(salt, entity [32]byte, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	var out [32]byte
	copy(out[:], crypto.Keccak256(softDetailsWords(salt, entity, operatorID, occurredAtMs)))
	return out
}

// softItemsHash is keccak256 of the concatenated itemIds, itemId = keccak256
// (lowercase uuid string) (ADR-0001 §2/§3); empty input equals keccak256("").
func softItemsHash(itemIDs []uuid.UUID) [32]byte {
	buf := make([]byte, 0, 32*len(itemIDs))
	for _, id := range itemIDs {
		buf = append(buf, crypto.Keccak256([]byte(id.String()))...)
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

// ── signed assembly unload (shipping-buffer scan) DTOs and helpers ───────────

// shippingBufferData models the submit response of the signed unload: the old
// shipping-buffer response plus the operation id.
type shippingBufferData struct {
	BufferBinID     string `json:"buffer_bin_id"`
	ProductsPlaced  int    `json:"products_placed"`
	OrdersAssembled int    `json:"orders_assembled"`
	OperationID     string `json:"operation_id"`
}

// draftUnloadToShippingBuffer POSTs the draft endpoint of the signed unload.
func draftUnloadToShippingBuffer(t *testing.T, env *env, op *signedOperator, bufferBinID string) closeDraftDTO {
	t.Helper()
	var draft closeDraftDTO
	postJSON(t, env, op.token, "/assembly/scan-shipping-buffer/draft", map[string]string{
		"buffer_bin_id": bufferBinID,
	}, &draft)
	return draft
}

// draftUnloadToShippingBufferRaw POSTs the draft endpoint and returns the raw
// error envelope (status + code) instead of failing the test.
func draftUnloadToShippingBufferRaw(t *testing.T, env *env, token, bufferBinID string) (int, string) {
	t.Helper()
	status, code, _ := postExpectError(t, env, token, "/assembly/scan-shipping-buffer/draft", map[string]string{
		"buffer_bin_id": bufferBinID,
	})
	return status, code
}

// submitUnloadToShippingBuffer POSTs the submit endpoint and requires 200.
func submitUnloadToShippingBuffer(t *testing.T, env *env, token, operationID string, assertions []map[string]string) shippingBufferData {
	t.Helper()
	var result shippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer/submit", map[string]any{
		"operation_id": operationID,
		"assertions":   assertions,
	}, &result)
	return result
}

// submitUnloadToShippingBufferRaw POSTs the submit endpoint and returns the raw
// error envelope (status + code) instead of failing the test.
func submitUnloadToShippingBufferRaw(t *testing.T, env *env, token, operationID string, assertions []map[string]string) (int, string) {
	t.Helper()
	status, code, _ := postExpectError(t, env, token, "/assembly/scan-shipping-buffer/submit", map[string]any{
		"operation_id": operationID,
		"assertions":   assertions,
	})
	return status, code
}

// unloadToShippingBufferSigned runs the whole signed unload: draft → sign → submit.
func unloadToShippingBufferSigned(t *testing.T, env *env, op *signedOperator, bufferBinID string) shippingBufferData {
	t.Helper()
	draft := draftUnloadToShippingBuffer(t, env, op, bufferBinID)
	assertions := signDraft(t, op, draft)
	return submitUnloadToShippingBuffer(t, env, op.token, draft.OperationID, assertions)
}

// softPickDetailsHash is an independent re-implementation of ADR-0001 §4 for
// Pick: keccak256(abi.encode(bytes32 salt, bytes16 shippingBufferBinId,
// bytes16[] orderIds, bytes16 operatorId, uint64 occurredAtMs)). Head: salt,
// bin (left-aligned), offset 0xa0, operator (left-aligned), uint64 (right-aligned);
// tail: length, one left-aligned bytes16 per orderId. orderIds go ascending by
// raw bytes without duplicates.
func softPickDetailsHash(salt [32]byte, binID uuid.UUID, orderIDs []uuid.UUID, operatorID uuid.UUID, occurredAtMs uint64) [32]byte {
	orders := sortedUniqueUUIDs(orderIDs)

	// Head: salt, bin, offset 0xa0 (5 static words), operator, uint64; tail:
	// length, one left-aligned bytes16 per orderId.
	words := make([]any, 0, 6+len(orders))
	words = append(words, salt, binID, 0xa0, operatorID, occurredAtMs, len(orders))
	for _, id := range orders {
		words = append(words, id)
	}
	var out [32]byte
	copy(out[:], crypto.Keccak256(softDetailsWords(words...)))
	return out
}

// sortedUniqueUUIDs returns a fresh slice sorted ascending by raw uuid bytes
// with duplicates removed; the input is not mutated.
func sortedUniqueUUIDs(ids []uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int {
		return bytes.Compare(a[:], b[:])
	})
	return slices.Compact(out)
}
