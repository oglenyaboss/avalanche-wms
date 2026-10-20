//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// registryABIJSON is the slice of ConsortiumRegistry.abi.json the test calls:
// owner txs (registerOrganization / revokeKey) and the getKey view.
const registryABIJSON = `[
  {"type":"function","name":"registerOrganization","inputs":[{"name":"orgId","type":"bytes16"},{"name":"name","type":"string"},{"name":"adminQx","type":"bytes32"},{"name":"adminQy","type":"bytes32"}],"outputs":[],"stateMutability":"nonpayable"},
  {"type":"function","name":"revokeKey","inputs":[{"name":"keyId","type":"bytes32"}],"outputs":[],"stateMutability":"nonpayable"},
  {"type":"function","name":"getKey","inputs":[{"name":"keyId","type":"bytes32"}],"outputs":[{"name":"key","type":"tuple","components":[{"name":"qx","type":"bytes32"},{"name":"qy","type":"bytes32"},{"name":"orgId","type":"bytes16"},{"name":"role","type":"uint8"},{"name":"active","type":"bool"}]}],"stateMutability":"view"}
]`

// registryKeyView mirrors ConsortiumRegistry.Key as unpacked by go-ethereum
// (ABI component names through abi.ToCamelCase).
type registryKeyView struct {
	Qx     [32]byte
	Qy     [32]byte
	OrgId  [16]byte
	Role   uint8
	Active bool
}

// Role enum values of ConsortiumRegistry.
const (
	roleNone     uint8 = 0
	roleOperator uint8 = 2
)

// keyActionDraftDTO models the draft response of contract §5.
type keyActionDraftDTO struct {
	ActionID   string `json:"action_id"`
	ActionType int    `json:"action_type"`
	SubjectKey struct {
		QX string `json:"qx"`
		QY string `json:"qy"`
	} `json:"subject_key"`
	Challenge        string `json:"challenge"`
	AllowCredentials []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"allow_credentials"`
	RPID             string `json:"rp_id"`
	UserVerification string `json:"user_verification"`
	Timeout          int    `json:"timeout"`
}

// confirmationDTO models the submit response and members[].confirmation.
type confirmationDTO struct {
	ActionID     string `json:"action_id"`
	ActionType   int    `json:"action_type"`
	State        string `json:"state"`
	RejectReason *int16 `json:"reject_reason"`
}

type organizationDTO struct {
	Organization struct {
		OrgID string `json:"org_id"`
		Name  string `json:"name"`
	} `json:"organization"`
	Members []orgMemberDTO `json:"members"`
}

type orgMemberDTO struct {
	UserID       string           `json:"user_id"`
	Username     string           `json:"username"`
	Role         string           `json:"role"`
	Passkey      *passkeyDTO      `json:"passkey"`
	Confirmation *confirmationDTO `json:"confirmation"`
}

type passkeyDTO struct {
	CredentialID string `json:"credential_id"`
	KeyID        string `json:"key_id"`
	Status       string `json:"status"`
}

// keyActionFixture holds the per-run org, users and passkeys for the key
// confirmation flow.
type keyActionFixture struct {
	suffix    string
	orgID     uuid.UUID
	orgName   string
	password  string
	adminUser string
	op1User   string
	op2User   string

	adminUserID uuid.UUID
	op1UserID   uuid.UUID
	op2UserID   uuid.UUID

	adminKey *ecdsa.PrivateKey
	op1Key   *ecdsa.PrivateKey
	op2Key   *ecdsa.PrivateKey

	adminCred []byte
	op1Cred   []byte
	op2Cred   []byte

	adminQX, adminQY [32]byte
	op1QX, op1QY     [32]byte
	op2QX, op2QY     [32]byte

	adminKeyID [32]byte
	op1KeyID   [32]byte
	op2KeyID   [32]byte
}

// newKeyActionFixture inserts a fresh organization, an ADMIN user with an
// ACTIVE-bound passkey and two OPERATOR users with PENDING passkeys. All rows
// are cleaned up in FK order; nothing collides with previous runs.
func newKeyActionFixture(t *testing.T, ctx context.Context, env *env) *keyActionFixture {
	t.Helper()

	fx := &keyActionFixture{
		suffix:    shortSuffix(),
		orgID:     uuid.New(),
		password:  "e2e-pass-123",
		adminUser: "admin-" + shortSuffix(),
		op1User:   "op1-" + shortSuffix(),
		op2User:   "op2-" + shortSuffix(),
	}
	fx.orgName = "e2e-org-" + fx.suffix
	fx.adminUserID, fx.op1UserID, fx.op2UserID = uuid.New(), uuid.New(), uuid.New()
	fx.adminKey, fx.op1Key, fx.op2Key = newSoftKey(), newSoftKey(), newSoftKey()
	fx.adminCred, fx.op1Cred, fx.op2Cred = randomCredID(t), randomCredID(t), randomCredID(t)
	fx.adminQX, fx.adminQY = softCoordinates(fx.adminKey)
	fx.op1QX, fx.op1QY = softCoordinates(fx.op1Key)
	fx.op2QX, fx.op2QY = softCoordinates(fx.op2Key)
	fx.adminKeyID = softKeyID(fx.adminQX, fx.adminQY)
	fx.op1KeyID = softKeyID(fx.op1QX, fx.op1QY)
	fx.op2KeyID = softKeyID(fx.op2QX, fx.op2QY)

	mustExec(t, ctx, env, `INSERT INTO public.organizations (org_id, name) VALUES ($1, $2)`, fx.orgID, fx.orgName)
	insertE2EUser := func(id uuid.UUID, username, role string) {
		mustExec(t, ctx, env, `
			INSERT INTO public.users (user_id, username, password_hash, role, is_active, org_id)
			VALUES ($1, $2, crypt($3, gen_salt('bf')), $4::public.user_role, true, $5)`,
			id, username, fx.password, role, fx.orgID)
	}
	insertE2EUser(fx.adminUserID, fx.adminUser, "ADMIN")
	insertE2EUser(fx.op1UserID, fx.op1User, "OPERATOR")
	insertE2EUser(fx.op2UserID, fx.op2User, "OPERATOR")

	insertE2EPasskey := func(cred []byte, userID uuid.UUID, role string, qx, qy, keyID [32]byte) {
		mustExec(t, ctx, env, `
			INSERT INTO public.passkey_credentials (credential_id, user_id, org_id, qx, qy, key_id, role, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'PENDING')`,
			cred, userID, fx.orgID, qx[:], qy[:], keyID[:], role)
	}
	insertE2EPasskey(fx.adminCred, fx.adminUserID, "ADMIN", fx.adminQX, fx.adminQY, fx.adminKeyID)
	insertE2EPasskey(fx.op1Cred, fx.op1UserID, "OPERATOR", fx.op1QX, fx.op1QY, fx.op1KeyID)
	insertE2EPasskey(fx.op2Cred, fx.op2UserID, "OPERATOR", fx.op2QX, fx.op2QY, fx.op2KeyID)

	t.Cleanup(func() {
		if os.Getenv("E2E_KEEP_FIXTURE") == "true" {
			return
		}
		cleanupCtx := context.Background()
		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM public.onchain_events WHERE event_id IN (SELECT action_id FROM public.key_actions WHERE org_id = $1)`, []any{fx.orgID}},
			{`DELETE FROM public.outbox_events WHERE event_id IN (SELECT action_id FROM public.key_actions WHERE org_id = $1)`, []any{fx.orgID}},
			{`DELETE FROM public.key_actions WHERE org_id = $1`, []any{fx.orgID}},
			{`DELETE FROM public.passkey_credentials WHERE org_id = $1`, []any{fx.orgID}},
			{`DELETE FROM public.users WHERE org_id = $1`, []any{fx.orgID}},
			{`DELETE FROM public.organizations WHERE org_id = $1`, []any{fx.orgID}},
		}
		for _, s := range stmts {
			if _, err := env.db.Exec(cleanupCtx, s.sql, s.args...); err != nil {
				t.Logf("newKeyActionFixture cleanup (%s): %v", s.sql, err)
			}
		}
	})
	return fx
}

func randomCredID(t *testing.T) []byte {
	t.Helper()
	cred := make([]byte, 16)
	_, err := rand.Read(cred)
	require.NoError(t, err)
	return cred
}

// ownerKeyForTest reads the registry owner (deployer) key from the shared state
// volume. The key value is never logged.
func ownerKeyForTest(t *testing.T, ctx context.Context) *ecdsa.PrivateKey {
	t.Helper()
	hexKey, err := readSharedStateFile(ctx, "deployer_key.txt")
	require.NoError(t, err)
	key, err := crypto.HexToECDSA(strings.TrimPrefix(hexKey, "0x"))
	require.NoError(t, err)
	return key
}

// sendOwnerRegistryTx sends a signed tx to the registry from the owner key and
// blocks until a successful receipt (registry-cli sendOwnerTx pattern).
func sendOwnerRegistryTx(t *testing.T, ctx context.Context, env *env, key *ecdsa.PrivateKey, to common.Address, data []byte) common.Hash {
	t.Helper()

	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()

	from := crypto.PubkeyToAddress(key.PublicKey)
	chainID, err := client.ChainID(ctx)
	require.NoError(t, err, "chain id")
	nonce, err := client.PendingNonceAt(ctx, from)
	require.NoError(t, err, "nonce")
	gasPrice, err := client.SuggestGasPrice(ctx)
	require.NoError(t, err, "gas price")
	gas, err := client.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Data: data})
	require.NoError(t, err, "estimate gas (OWNER_KEY не владелец реестра?)")
	signed, err := types.SignTx(types.NewTransaction(nonce, to, big.NewInt(0), gas, gasPrice, data), types.NewEIP155Signer(chainID), key)
	require.NoError(t, err, "sign tx")
	require.NoError(t, client.SendTransaction(ctx, signed), "send tx")

	deadline := time.Now().Add(120 * time.Second)
	for {
		r, err := client.TransactionReceipt(ctx, signed.Hash())
		if err == nil {
			require.Equal(t, types.ReceiptStatusSuccessful, r.Status, "owner tx %s reverted", signed.Hash().Hex())
			return signed.Hash()
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("owner tx %s: no receipt in 120s", signed.Hash().Hex())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("owner tx %s: context done waiting receipt: %v", signed.Hash().Hex(), ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// registerOrgOnChain calls owner-only registerOrganization(orgId, name, qx, qy),
// which also registers the admin key (Role.Admin, active).
func registerOrgOnChain(t *testing.T, ctx context.Context, env *env, key *ecdsa.PrivateKey, registryAddr common.Address, fx *keyActionFixture) {
	t.Helper()
	parsed, err := abi.JSON(strings.NewReader(registryABIJSON))
	require.NoError(t, err)
	data, err := parsed.Pack("registerOrganization", [16]byte(fx.orgID), fx.orgName, fx.adminQX, fx.adminQY)
	require.NoError(t, err)
	sendOwnerRegistryTx(t, ctx, env, key, registryAddr, data)
}

// revokeKeyOnChain calls owner-only revokeKey(bytes32).
func revokeKeyOnChain(t *testing.T, ctx context.Context, env *env, key *ecdsa.PrivateKey, registryAddr common.Address, keyID [32]byte) {
	t.Helper()
	parsed, err := abi.JSON(strings.NewReader(registryABIJSON))
	require.NoError(t, err)
	data, err := parsed.Pack("revokeKey", keyID)
	require.NoError(t, err)
	sendOwnerRegistryTx(t, ctx, env, key, registryAddr, data)
}

// callRegistryKey reads the getKey(bytes32) view.
func callRegistryKey(t *testing.T, ctx context.Context, env *env, registryAddr string, keyID [32]byte) registryKeyView {
	t.Helper()
	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()

	parsed, err := abi.JSON(strings.NewReader(registryABIJSON))
	require.NoError(t, err)
	data, err := parsed.Pack("getKey", keyID)
	require.NoError(t, err)
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   ptr(common.HexToAddress(registryAddr)),
		Data: data,
	}, nil)
	require.NoError(t, err)

	values, err := parsed.Unpack("getKey", result)
	require.NoError(t, err)
	require.Len(t, values, 1)

	raw, err := json.Marshal(values[0])
	require.NoError(t, err)
	var out registryKeyView
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// waitForOnchainKeyAction blocks until the onchain_events row for the action id
// reaches wantStatus with the wanted reject_reason (nil = must be NULL).
func waitForOnchainKeyAction(t *testing.T, ctx context.Context, env *env, actionID uuid.UUID, wantStatus string, wantReason *int16) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assertCollect) {
		var status string
		var reason *int16
		err := env.db.QueryRow(ctx, `
			SELECT status::text, reject_reason
			FROM public.onchain_events
			WHERE event_id = $1`, actionID).Scan(&status, &reason)
		require.NoError(c, err)
		require.Equal(c, wantStatus, status)
		if wantReason == nil {
			require.Nil(c, reason)
		} else {
			require.NotNil(c, reason)
			if reason != nil {
				require.Equal(c, *wantReason, *reason)
			}
		}
	}, 90*time.Second, 2*time.Second, "onchain_events for action %s did not reach %s", actionID, wantStatus)
}

func outboxCountForEvent(t *testing.T, ctx context.Context, env *env, eventID uuid.UUID) int {
	t.Helper()
	var count int
	err := env.db.QueryRow(ctx,
		`SELECT count(*) FROM public.outbox_events WHERE event_id = $1`, eventID).Scan(&count)
	require.NoError(t, err)
	return count
}

// fetchOrganization GETs /passkeys/organization without failing the test on
// transient errors, so it can be used inside a poll loop.
func fetchOrganization(ctx context.Context, env *env, token string) (organizationDTO, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.wmsURL+"/passkeys/organization", nil)
	if err != nil {
		return organizationDTO{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.httpClient.Do(req)
	if err != nil {
		return organizationDTO{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return organizationDTO{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return organizationDTO{}, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    organizationDTO `json:"data"`
		Error   *apiError       `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return organizationDTO{}, err
	}
	if !envelope.Success || envelope.Error != nil {
		return organizationDTO{}, fmt.Errorf("api error in body: %s", body)
	}
	return envelope.Data, nil
}

// waitForOrganization polls the organization view until want holds, returning
// the last view; on timeout it fails with the last view dumped.
func waitForOrganization(t *testing.T, ctx context.Context, env *env, token string, timeout time.Duration, want func(organizationDTO) bool) organizationDTO {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last organizationDTO
	for {
		if org, err := fetchOrganization(ctx, env, token); err == nil {
			last = org
			if want(org) {
				return org
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			raw, _ := json.Marshal(last)
			t.Fatalf("organization view did not reach the expected state in %s: %s", timeout, raw)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("organization poll interrupted: %v", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func memberByUsername(org organizationDTO, username string) (orgMemberDTO, bool) {
	for _, m := range org.Members {
		if m.Username == username {
			return m, true
		}
	}
	return orgMemberDTO{}, false
}

func int16Ref(v int16) *int16 { return &v }

// TestKeyConfirmation_EndToEnd drives the admin confirmation of an operator
// passkey end to end: WMS draft/submit API (software P-256 signer) -> outbox ->
// Debezium (payload column) -> Kafka -> ledger-adapter -> ConsortiumRegistry,
// plus the repeat-submit guard and a contract rejection with its reason.
func TestKeyConfirmation_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	fx := newKeyActionFixture(t, ctx, env)
	owner := ownerKeyForTest(t, ctx)
	registryHex, err := readSharedStateFile(ctx, "registry_addr.txt")
	require.NoError(t, err)
	registryAddr := common.HexToAddress(registryHex)

	// 1. Owner registers the organization together with the admin key.
	registerOrgOnChain(t, ctx, env, owner, registryAddr, fx)

	// 2. The admin's own passkey becomes ACTIVE through the registry status sync.
	adminToken := loginAs(t, env, fx.adminUser, fx.password)
	waitForOrganization(t, ctx, env, adminToken, 60*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, fx.adminUser)
		return ok && m.Passkey != nil && m.Passkey.Status == "ACTIVE"
	})

	// 3. Draft a KeyRegister action for op1 and check it against ADR-0001 §3.
	var draft keyActionDraftDTO
	postJSON(t, env, adminToken, "/passkeys/actions/key-register/draft", map[string]string{
		"target_key_id": "0x" + hex.EncodeToString(fx.op1KeyID[:]),
	}, &draft)
	require.Equal(t, 5, draft.ActionType)
	require.Equal(t, "localhost", draft.RPID)
	require.Equal(t, "required", draft.UserVerification)

	actionID, err := uuid.Parse(draft.ActionID)
	require.NoError(t, err)
	var noItems [32]byte
	copy(noItems[:], softNoItems)
	wantChallenge := softChallenge(actionID, 5, noItems, softKeyID(fx.op1QX, fx.op1QY))
	challengeBytes, err := base64.RawURLEncoding.DecodeString(draft.Challenge)
	require.NoError(t, err)
	require.Equal(t, wantChallenge[:], challengeBytes, "draft challenge is not the ADR-0001 challenge")

	require.NotEmpty(t, draft.AllowCredentials)
	require.Equal(t, "public-key", draft.AllowCredentials[0].Type)
	require.Equal(t, base64.RawURLEncoding.EncodeToString(fx.adminCred), draft.AllowCredentials[0].ID)

	// 4. Sign with the software signer and submit; WMS stores SENT.
	assertion := softSign(fx.adminKey, wantChallenge)
	var sent confirmationDTO
	postJSON(t, env, adminToken, "/passkeys/actions/"+draft.ActionID+"/submit", map[string]string{
		"credential_id":      base64.RawURLEncoding.EncodeToString(fx.adminCred),
		"authenticator_data": base64.RawURLEncoding.EncodeToString(assertion.AuthenticatorData),
		"client_data_json":   base64.RawURLEncoding.EncodeToString(assertion.ClientDataJSON),
		"signature":          base64.RawURLEncoding.EncodeToString(assertion.Signature),
	}, &sent)
	require.Equal(t, "SENT", sent.State)

	// 5. The adapter applies the action on chain; op1 becomes an active operator.
	waitForOnchainKeyAction(t, ctx, env, actionID, "COMMITTED", nil)
	key := callRegistryKey(t, ctx, env, registryHex, fx.op1KeyID)
	require.Equal(t, roleOperator, key.Role, "op1 on-chain role")
	require.True(t, key.Active, "op1 on-chain active")
	require.Equal(t, [16]byte(fx.orgID), key.OrgId, "op1 on-chain org")

	org := waitForOrganization(t, ctx, env, adminToken, 30*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, fx.op1User)
		return ok && m.Passkey != nil && m.Passkey.Status == "ACTIVE" &&
			m.Confirmation != nil && m.Confirmation.State == "APPLIED"
	})
	op1, _ := memberByUsername(org, fx.op1User)
	require.Equal(t, draft.ActionID, op1.Confirmation.ActionID)
	require.Nil(t, op1.Confirmation.RejectReason)

	// 6. Repeating the submit is rejected and creates no duplicate outbox row.
	status, code, _ := postExpectError(t, env, adminToken, "/passkeys/actions/"+draft.ActionID+"/submit", map[string]string{
		"credential_id":      base64.RawURLEncoding.EncodeToString(fx.adminCred),
		"authenticator_data": base64.RawURLEncoding.EncodeToString(assertion.AuthenticatorData),
		"client_data_json":   base64.RawURLEncoding.EncodeToString(assertion.ClientDataJSON),
		"signature":          base64.RawURLEncoding.EncodeToString(assertion.Signature),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "ACTION_ALREADY_SUBMITTED", code)
	require.Equal(t, 1, outboxCountForEvent(t, ctx, env, actionID))

	// 7. Rejection path for op2: draft and sign BEFORE the admin key is revoked,
	//    revoke, then submit — WMS accepts (its DB still says ACTIVE), the
	//    contract rejects with KeyRevoked (reason 2, ADR-0001 §6/§7).
	var draft2 keyActionDraftDTO
	postJSON(t, env, adminToken, "/passkeys/actions/key-register/draft", map[string]string{
		"target_key_id": "0x" + hex.EncodeToString(fx.op2KeyID[:]),
	}, &draft2)
	actionID2, err := uuid.Parse(draft2.ActionID)
	require.NoError(t, err)
	challenge2Bytes, err := base64.RawURLEncoding.DecodeString(draft2.Challenge)
	require.NoError(t, err)
	var challenge2 [32]byte
	copy(challenge2[:], challenge2Bytes)
	assertion2 := softSign(fx.adminKey, challenge2)

	revokeKeyOnChain(t, ctx, env, owner, registryAddr, fx.adminKeyID)

	var sent2 confirmationDTO
	postJSON(t, env, adminToken, "/passkeys/actions/"+draft2.ActionID+"/submit", map[string]string{
		"credential_id":      base64.RawURLEncoding.EncodeToString(fx.adminCred),
		"authenticator_data": base64.RawURLEncoding.EncodeToString(assertion2.AuthenticatorData),
		"client_data_json":   base64.RawURLEncoding.EncodeToString(assertion2.ClientDataJSON),
		"signature":          base64.RawURLEncoding.EncodeToString(assertion2.Signature),
	}, &sent2)
	require.Equal(t, "SENT", sent2.State)

	waitForOnchainKeyAction(t, ctx, env, actionID2, "FAILED", int16Ref(2))

	org2 := waitForOrganization(t, ctx, env, adminToken, 30*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, fx.op2User)
		return ok && m.Passkey != nil && m.Passkey.Status == "PENDING" &&
			m.Confirmation != nil && m.Confirmation.State == "REJECTED" &&
			m.Confirmation.RejectReason != nil && *m.Confirmation.RejectReason == 2
	})
	op2, _ := memberByUsername(org2, fx.op2User)
	require.Equal(t, draft2.ActionID, op2.Confirmation.ActionID)
	require.Equal(t, 1, outboxCountForEvent(t, ctx, env, actionID2))

	op2Key := callRegistryKey(t, ctx, env, registryHex, fx.op2KeyID)
	require.Equal(t, roleNone, op2Key.Role, "op2 must stay unregistered")
	require.False(t, op2Key.Active, "op2 must stay inactive")
}

// TestKeyConfirmation_SecondDraftRejectedAfterSubmit checks the double-submit
// guard: two drafts for the same pending operator key coexist while nothing is
// submitted, but once the first draft is submitted, submitting the second fails
// with ACTION_IN_PROGRESS instead of creating a second in-flight action.
func TestKeyConfirmation_SecondDraftRejectedAfterSubmit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	env := testEnv
	require.NotNil(t, env)

	fx := newKeyActionFixture(t, ctx, env)
	owner := ownerKeyForTest(t, ctx)
	registryHex, err := readSharedStateFile(ctx, "registry_addr.txt")
	require.NoError(t, err)
	registryAddr := common.HexToAddress(registryHex)

	// The admin's own passkey must be ACTIVE (via registry status sync) before
	// the submit repository transaction accepts the signer.
	registerOrgOnChain(t, ctx, env, owner, registryAddr, fx)

	adminToken := loginAs(t, env, fx.adminUser, fx.password)
	waitForOrganization(t, ctx, env, adminToken, 60*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, fx.adminUser)
		return ok && m.Passkey != nil && m.Passkey.Status == "ACTIVE"
	})

	// 1. Two drafts for the same pending operator key both succeed: nothing is
	//    submitted yet, so no confirmation is in flight.
	targetKeyID := "0x" + hex.EncodeToString(fx.op1KeyID[:])
	var draft1, draft2 keyActionDraftDTO
	postJSON(t, env, adminToken, "/passkeys/actions/key-register/draft", map[string]string{
		"target_key_id": targetKeyID,
	}, &draft1)
	postJSON(t, env, adminToken, "/passkeys/actions/key-register/draft", map[string]string{
		"target_key_id": targetKeyID,
	}, &draft2)
	require.NotEqual(t, draft1.ActionID, draft2.ActionID)

	// submitBody signs the draft's challenge with the software admin key and
	// builds the submit request body.
	submitBody := func(t *testing.T, draft keyActionDraftDTO) map[string]string {
		t.Helper()
		challengeBytes, err := base64.RawURLEncoding.DecodeString(draft.Challenge)
		require.NoError(t, err)
		var challenge [32]byte
		copy(challenge[:], challengeBytes)
		assertion := softSign(fx.adminKey, challenge)
		return map[string]string{
			"credential_id":      base64.RawURLEncoding.EncodeToString(fx.adminCred),
			"authenticator_data": base64.RawURLEncoding.EncodeToString(assertion.AuthenticatorData),
			"client_data_json":   base64.RawURLEncoding.EncodeToString(assertion.ClientDataJSON),
			"signature":          base64.RawURLEncoding.EncodeToString(assertion.Signature),
		}
	}

	// 2. Submitting the first draft succeeds.
	var sent confirmationDTO
	postJSON(t, env, adminToken, "/passkeys/actions/"+draft1.ActionID+"/submit", submitBody(t, draft1), &sent)
	require.Equal(t, "SENT", sent.State)

	// 3. Submitting the second draft is rejected: the first action is in flight.
	status, code, message := postExpectError(t, env, adminToken, "/passkeys/actions/"+draft2.ActionID+"/submit", submitBody(t, draft2))
	require.Equal(t, http.StatusConflict, status, "second submit: %s", message)
	require.Equal(t, "ACTION_IN_PROGRESS", code)
}
