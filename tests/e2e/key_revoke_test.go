//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// submitKeyActionSoft submits a key action signed by the software signer and
// returns the confirmation view.
func submitKeyActionSoft(t *testing.T, env *env, token string, credID []byte, actionID string, a softAssertion) confirmationDTO {
	t.Helper()
	var sent confirmationDTO
	postJSON(t, env, token, "/passkeys/actions/"+actionID+"/submit", map[string]string{
		"credential_id":      base64.RawURLEncoding.EncodeToString(credID),
		"authenticator_data": base64.RawURLEncoding.EncodeToString(a.AuthenticatorData),
		"client_data_json":   base64.RawURLEncoding.EncodeToString(a.ClientDataJSON),
		"signature":          base64.RawURLEncoding.EncodeToString(a.Signature),
	}, &sent)
	return sent
}

// revokeOperatorKey runs the admin key-revoke flow for the operator's key:
// draft → soft sign with the shared admin key → submit → wait COMMITTED.
// Returns the revoke action id.
func revokeOperatorKey(t *testing.T, ctx context.Context, env *env, op *signedOperator) uuid.UUID {
	t.Helper()
	shared := requireConsortium(t, ctx, env)

	var draft keyActionDraftDTO
	postJSON(t, env, shared.adminToken, "/passkeys/actions/key-revoke/draft", map[string]string{
		"target_key_id": "0x" + hex.EncodeToString(op.keyID[:]),
	}, &draft)
	require.Equal(t, 6, draft.ActionType, "key-revoke draft action_type")

	actionID, err := uuid.Parse(draft.ActionID)
	require.NoError(t, err)
	challengeBytes, err := base64.RawURLEncoding.DecodeString(draft.Challenge)
	require.NoError(t, err)
	var challenge [32]byte
	copy(challenge[:], challengeBytes)

	sent := submitKeyActionSoft(t, env, shared.adminToken, shared.adminCred, draft.ActionID, softSign(shared.adminKey, challenge))
	require.Equal(t, "SENT", sent.State)

	waitForOnchainKeyAction(t, ctx, env, actionID, "COMMITTED", nil)
	return actionID
}

// TestKeyRevoke_EndToEnd drives the admin revocation of an operator key end to
// end: key-revoke draft (challenge checked against ADR-0001 §3) → submit →
// chain COMMITTED → registry inactive → REVOKED in the org listing, and the
// backend refuses the operator's next warehouse signature.
func TestKeyRevoke_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	shared := requireConsortium(t, ctx, env)
	op := newSignedOperator(t, ctx, env)

	// A cargoplace in progress, so the operator has a close to attempt afterwards.
	sc := setupReceivingScaffold(t, ctx, env, 1)
	receiveAndCloseSignedPrep(t, ctx, env, op, sc)

	// 1. Draft the KeyRevoke action and check the challenge against ADR-0001 §3.
	var draft keyActionDraftDTO
	postJSON(t, env, shared.adminToken, "/passkeys/actions/key-revoke/draft", map[string]string{
		"target_key_id": "0x" + hex.EncodeToString(op.keyID[:]),
	}, &draft)
	require.Equal(t, 6, draft.ActionType)

	actionID, err := uuid.Parse(draft.ActionID)
	require.NoError(t, err)
	var noItems [32]byte
	copy(noItems[:], softNoItems)
	qx, qy := softCoordinates(op.key)
	wantChallenge := softChallenge(actionID, 6, noItems, softKeyID(qx, qy))
	challengeBytes, err := base64.RawURLEncoding.DecodeString(draft.Challenge)
	require.NoError(t, err)
	require.Equal(t, wantChallenge[:], challengeBytes, "revoke draft challenge is not the ADR-0001 challenge")

	// 2. Sign with the shared admin key and submit; WMS stores SENT.
	sent := submitKeyActionSoft(t, env, shared.adminToken, shared.adminCred, draft.ActionID, softSign(shared.adminKey, wantChallenge))
	require.Equal(t, "SENT", sent.State)
	require.Equal(t, 6, sent.ActionType)

	// 3. The adapter applies the revoke; the key is inactive in the registry.
	waitForOnchainKeyAction(t, ctx, env, actionID, "COMMITTED", nil)
	registryHex, err := readSharedStateFile(ctx, "registry_addr.txt")
	require.NoError(t, err)
	key := callRegistryKey(t, ctx, env, registryHex, op.keyID)
	require.False(t, key.Active, "operator key must be inactive in the registry after the revoke")

	// 4. The org listing shows the operator passkey REVOKED with the revoke confirmation.
	org := waitForOrganization(t, ctx, env, shared.adminToken, 30*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, op.username)
		return ok && m.Passkey != nil && m.Passkey.Status == "REVOKED" &&
			m.Confirmation != nil && m.Confirmation.State == "APPLIED" && m.Confirmation.ActionType == 6
	})
	member, _ := memberByUsername(org, op.username)
	require.Equal(t, draft.ActionID, member.Confirmation.ActionID)

	// 5. The backend refuses the operator's next warehouse signature.
	status, code, _ := postExpectError(t, env, op.token, "/receiving/table/close-cargoplace/draft", map[string]string{
		"cargoplace_id": sc.CargoplaceID.String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "SIGNER_KEY_NOT_ACTIVE", code)
}

// receiveEnvelope builds a signed Receive envelope (ADR-0001 §9, version 1) over
// n fresh items, sorted by itemId ascending (the contract rejects
// ItemsNotAscending). Returns the action id, the sorted items, the challenge the
// envelope signs and the marshalled envelope (the outbox/Kafka payload).
func receiveEnvelope(t *testing.T, key *ecdsa.PrivateKey, keyID [32]byte, n int) (actionID uuid.UUID, items []uuid.UUID, challenge [32]byte, payload []byte) {
	t.Helper()

	actionID = uuid.New()
	items = make([]uuid.UUID, n)
	for i := range items {
		items[i] = uuid.New()
	}
	sort.Slice(items, func(i, j int) bool {
		return uuidToUint256(items[i].String()).Cmp(uuidToUint256(items[j].String())) < 0
	})
	itemIDs := make([]string, 0, len(items))
	for _, id := range items {
		itemIDs = append(itemIDs, id.String())
	}

	var detailsHash [32]byte
	_, err := rand.Read(detailsHash[:])
	require.NoError(t, err)
	challenge = softChallenge(actionID, 1, softItemsHash(items), detailsHash)
	a := softSign(key, challenge)

	cdj := string(a.ClientDataJSON)
	typeIndex := strings.Index(cdj, `"type":"webauthn.get"`)
	challengeIndex := strings.Index(cdj, `"challenge":"`)
	require.GreaterOrEqual(t, typeIndex, 0, "clientDataJSON lacks type")
	require.GreaterOrEqual(t, challengeIndex, 0, "clientDataJSON lacks challenge")

	var sig struct{ R, S *big.Int }
	_, err = asn1.Unmarshal(a.Signature, &sig)
	require.NoError(t, err)
	s := sig.S
	if s.Cmp(new(big.Int).Rsh(key.Curve.Params().N, 1)) > 0 {
		s = new(big.Int).Sub(key.Curve.Params().N, s)
	}

	payload, err = json.Marshal(map[string]any{
		"version":     1,
		"actionId":    actionID.String(),
		"actionType":  1,
		"itemIds":     itemIDs,
		"detailsHash": "0x" + hex.EncodeToString(detailsHash[:]),
		"keyId":       "0x" + hex.EncodeToString(keyID[:]),
		"webauthn": map[string]any{
			"authenticatorData": "0x" + hex.EncodeToString(a.AuthenticatorData),
			"clientDataJSON":    cdj,
			"challengeIndex":    challengeIndex,
			"typeIndex":         typeIndex,
			"r":                 "0x" + hex.EncodeToString(sig.R.FillBytes(make([]byte, 32))),
			"s":                 "0x" + hex.EncodeToString(s.FillBytes(make([]byte, 32))),
		},
	})
	require.NoError(t, err)
	return actionID, items, challenge, payload
}

// insertBypassReceive builds a signed Receive envelope with the operator's key
// and inserts it directly into public.outbox_events, bypassing the WMS backend.
// Returns the action id.
func insertBypassReceive(t *testing.T, ctx context.Context, env *env, op *signedOperator) uuid.UUID {
	t.Helper()

	actionID, _, challenge, payload := receiveEnvelope(t, op.key, op.keyID, 2)
	mustExec(t, ctx, env, `
		INSERT INTO public.outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash, payload)
		VALUES ($1, $1, 'signed_action', 'wms.action.v1', $2, $3::jsonb)`,
		actionID, hex.EncodeToString(challenge[:]), string(payload))
	return actionID
}

// TestKeyRevoke_BypassActionRejectedByContract proves the contract itself
// refuses a warehouse action signed by a revoked key: the envelope is inserted
// into the outbox bypassing the backend and the chain rejects it with
// KeyRevoked (reason 2, ADR-0001 §6).
func TestKeyRevoke_BypassActionRejectedByContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	revokeOperatorKey(t, ctx, env, op)

	actionID := insertBypassReceive(t, ctx, env, op)
	waitForOnchainKeyAction(t, ctx, env, actionID, "FAILED", int16Ref(2))
}

// TestKeyRevoke_SerializesWithWarehouseAction proves ADR-0001 §7: (a) an action
// committed before the revoke goes to the outbox and on chain before it and is
// applied; (b) a revoke that commits while a submit waits on the key row makes
// that submit fail with SIGNER_KEY_NOT_ACTIVE and write nothing.
func TestKeyRevoke_SerializesWithWarehouseAction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	shared := requireConsortium(t, ctx, env)

	// ── (a) action committed before the revoke ──
	op := newSignedOperator(t, ctx, env)
	sc := setupReceivingScaffold(t, ctx, env, 1)
	receiveAndCloseSignedPrep(t, ctx, env, op, sc)
	draft := draftCloseCargoplace(t, env, op, sc.CargoplaceID.String())
	assertions := signDraft(t, op, draft)
	require.Len(t, draft.Parts, 1)
	closeActionID, err := uuid.Parse(draft.Parts[0].ActionID)
	require.NoError(t, err)

	result := submitCloseCargoplace(t, env, op.token, draft.OperationID, assertions)
	require.Equal(t, "TABLE_CLOSED", result.Status)

	revokeID := revokeOperatorKey(t, ctx, env, op)

	var closeOutboxID, revokeOutboxID int64
	require.NoError(t, env.db.QueryRow(ctx,
		`SELECT id FROM public.outbox_events WHERE event_id = $1`, closeActionID).Scan(&closeOutboxID))
	require.NoError(t, env.db.QueryRow(ctx,
		`SELECT id FROM public.outbox_events WHERE event_id = $1`, revokeID).Scan(&revokeOutboxID))
	require.Less(t, closeOutboxID, revokeOutboxID, "the close action must enter the outbox before the revoke")

	waitForOnchainCommitted(t, ctx, env, closeActionID)
	waitForOperationState(t, ctx, env, op.token, draft.OperationID, "APPLIED", nil)
	waitForOnchainKeyAction(t, ctx, env, revokeID, "COMMITTED", nil)

	// ── (b) the revoke wins the lock ──
	op2 := newSignedOperator(t, ctx, env)
	sc2 := setupReceivingScaffold(t, ctx, env, 1)
	receiveAndCloseSignedPrep(t, ctx, env, op2, sc2)
	draft2 := draftCloseCargoplace(t, env, op2, sc2.CargoplaceID.String())
	assertions2 := signDraft(t, op2, draft2)
	require.Len(t, draft2.Parts, 1)
	part2ActionID, err := uuid.Parse(draft2.Parts[0].ActionID)
	require.NoError(t, err)

	body, err := json.Marshal(map[string]any{
		"operation_id": draft2.OperationID,
		"assertions":   assertions2,
	})
	require.NoError(t, err)

	// The test transaction holds the operator's key row FOR UPDATE; the submit
	// must block on LockSigner's FOR SHARE until this transaction commits.
	tx, err := env.db.Begin(ctx)
	require.NoError(t, err)
	if _, err := tx.Exec(ctx,
		`SELECT credential_id FROM public.passkey_credentials WHERE credential_id = $1 FOR UPDATE`,
		op2.credID); err != nil {
		_ = tx.Rollback(ctx)
		require.NoError(t, err)
	}

	type submitOutcome struct {
		status int
		code   string
		err    error
	}
	outcome := make(chan submitOutcome, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.wmsURL+"/receiving/table/close-cargoplace/submit", bytes.NewReader(body))
		if err != nil {
			outcome <- submitOutcome{err: err}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+op2.token)
		resp, err := env.httpClient.Do(req)
		if err != nil {
			outcome <- submitOutcome{err: err}
			return
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			outcome <- submitOutcome{err: err}
			return
		}
		var envelope struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		code := ""
		if envelope.Error != nil {
			code = envelope.Error.Code
		}
		outcome <- submitOutcome{status: resp.StatusCode, code: code}
	}()

	// Give the submit time to reach the FOR SHARE, then revoke under its nose:
	// a submitted KeyRevoke row for the operator's key, written bypassing the
	// backend (challenge bytes are arbitrary, no outbox row).
	time.Sleep(time.Second)
	fakeRevokeID := uuid.New()
	var fakeChallenge [32]byte
	_, err = rand.Read(fakeChallenge[:])
	require.NoError(t, err)
	if _, err := tx.Exec(ctx, `
		INSERT INTO public.key_actions
			(action_id, org_id, action_type, target_credential_id, signer_credential_id, challenge, created_by, submitted_at)
		VALUES ($1, $2, 6, $3, $4, $5, $6, now())`,
		fakeRevokeID, shared.orgID, op2.credID, shared.adminCred, fakeChallenge[:], shared.adminUserID); err != nil {
		_ = tx.Rollback(ctx)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit(ctx))
	t.Cleanup(func() {
		_, _ = env.db.Exec(context.Background(), `DELETE FROM public.key_actions WHERE action_id = $1`, fakeRevokeID)
	})

	select {
	case out := <-outcome:
		require.NoError(t, out.err)
		require.Equal(t, http.StatusConflict, out.status, "submit after the revoke must fail")
		require.Equal(t, "SIGNER_KEY_NOT_ACTIVE", out.code)
	case <-time.After(90 * time.Second):
		t.Fatal("submit did not return after the revoke committed")
	}

	var cpStatus string
	require.NoError(t, env.db.QueryRow(ctx, `
		SELECT status::text FROM wms_inventory.cargoplaces WHERE cargoplace_id = $1`,
		sc2.CargoplaceID).Scan(&cpStatus))
	require.Equal(t, "TABLE_IN_PROGRESS", cpStatus, "the blocked submit must not close the cargoplace")
	require.Equal(t, 0, outboxCountForEvent(t, ctx, env, part2ActionID), "no outbox row for the refused action")
}
