// stress-provision provisions a load-test consortium on a running dev/stress
// stack: it inserts the organization + ADMIN + N OPERATOR rows, registers the
// organization on chain via registry-cli, and confirms every passkey end to end
// (draft → soft sign → submit → ACTIVE), then writes the operators' credentials
// and private keys to tests/stress/.signed/operators.json for k6.
//
// The flow is copied from the signed-action e2e suite: softauthn_test.go (the
// software signer), signed_operator_test.go (rows + confirmation),
// key_confirmation_test.go (DTOs + organization polling), api_test.go (login +
// success envelope).

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	defaultOperators        = 70
	defaultOut              = "../stress/.signed"
	defaultDBURL            = "postgres://root:root@localhost:5432/wms_blockchain_db?sslmode=disable"
	defaultWMSURL           = "http://localhost:8081"
	defaultAdapterContainer = "ledger-adapter"
	defaultAdapterHealthURL = "http://localhost:8085/health"
)

// ── software P-256 signer, byte-identical to tests/e2e/softauthn_test.go ─────

const softOrigin = "http://localhost:5173"
const softRPID = "localhost"

// softAssertion is the browser assertion parts the WMS submit API takes.
type softAssertion struct {
	AuthenticatorData []byte
	ClientDataJSON    []byte
	Signature         []byte
}

// newSoftKey generates a random P-256 key, panicking on the impossible error.
func newSoftKey() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}

// softCoordinates returns the public key as the two ABI words qx and qy.
func softCoordinates(k *ecdsa.PrivateKey) (qx, qy [32]byte) {
	k.X.FillBytes(qx[:])
	k.Y.FillBytes(qy[:])
	return qx, qy
}

// softSign produces a browser-shaped assertion over the challenge.
func softSign(k *ecdsa.PrivateKey, challenge [32]byte) softAssertion {
	cdj := `{"type":"webauthn.get","challenge":"` +
		base64.RawURLEncoding.EncodeToString(challenge[:]) +
		`","origin":"` + softOrigin + `","crossOrigin":false}`

	rpHash := sha256.Sum256([]byte(softRPID))
	authData := append(rpHash[:], 0x05)
	authData = binary.BigEndian.AppendUint32(authData, 1)

	clientHash := sha256.Sum256([]byte(cdj))
	h := sha256.New()
	h.Write(authData)
	h.Write(clientHash[:])
	sig, err := ecdsa.SignASN1(rand.Reader, k, h.Sum(nil))
	if err != nil {
		panic(err)
	}
	return softAssertion{
		AuthenticatorData: authData,
		ClientDataJSON:    []byte(cdj),
		Signature:         sig,
	}
}

// softKeyID is keccak256(qx || qy), the registry key id.
func softKeyID(qx, qy [32]byte) [32]byte {
	var out [32]byte
	copy(out[:], crypto.Keccak256(append(append([]byte{}, qx[:]...), qy[:]...)))
	return out
}

// ── WMS DTOs, copied from tests/e2e/key_confirmation_test.go ─────────────────

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

func memberByUsername(org organizationDTO, username string) (orgMemberDTO, bool) {
	for _, m := range org.Members {
		if m.Username == username {
			return m, true
		}
	}
	return orgMemberDTO{}, false
}

// ── provisioning model ───────────────────────────────────────────────────────

// stressPassword is the shared password of the admin and every operator.
const stressPassword = "stress-pass-123"

// operator is one OPERATOR user with a pending P-256 passkey.
type operator struct {
	userID   uuid.UUID
	username string
	key      *ecdsa.PrivateKey
	credID   []byte
	qx, qy   [32]byte
	keyID    [32]byte
}

// provisioning is everything the run creates before any HTTP call.
type provisioning struct {
	orgID       uuid.UUID
	orgName     string
	adminUserID uuid.UUID
	adminUser   string
	adminKey    *ecdsa.PrivateKey
	adminCred   []byte
	adminQX     [32]byte
	adminQY     [32]byte
	adminKeyID  [32]byte
	operators   []operator
}

// newSuffix returns 6 random hex chars, the per-run uniqueness marker.
func newSuffix() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newCredID returns a random 16-byte credential id (the e2e randomCredID).
func newCredID() ([]byte, error) {
	cred := make([]byte, 16)
	if _, err := rand.Read(cred); err != nil {
		return nil, err
	}
	return cred, nil
}

// operatorUsernames builds the NN-suffixed operator names: 2 digits up to 99
// operators, 3 digits beyond.
func operatorUsernames(suffix string, n int) []string {
	width := 2
	if n > 99 {
		width = 3
	}
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("stress-sop-%s-%0*d", suffix, width, i+1)
	}
	return out
}

// newProvisioning generates the org, the admin and N operator keys + credential
// ids; usernames derive from the suffix.
func newProvisioning(suffix string, n int) (*provisioning, error) {
	p := &provisioning{
		orgID:       uuid.New(),
		orgName:     "stress-" + suffix,
		adminUserID: uuid.New(),
		adminUser:   "stress-admin-" + suffix,
	}
	p.adminKey = newSoftKey()
	p.adminQX, p.adminQY = softCoordinates(p.adminKey)
	p.adminKeyID = softKeyID(p.adminQX, p.adminQY)
	var err error
	if p.adminCred, err = newCredID(); err != nil {
		return nil, err
	}

	usernames := operatorUsernames(suffix, n)
	p.operators = make([]operator, n)
	for i := range p.operators {
		op := &p.operators[i]
		op.userID = uuid.New()
		op.username = usernames[i]
		op.key = newSoftKey()
		op.qx, op.qy = softCoordinates(op.key)
		op.keyID = softKeyID(op.qx, op.qy)
		if op.credID, err = newCredID(); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// ── postgres rows, copied from tests/e2e/signed_operator_test.go ─────────────

// insertRows inserts the organization, the ADMIN user + PENDING admin passkey
// and every OPERATOR user + PENDING passkey in ONE transaction.
func insertRows(ctx context.Context, dbURL string, p *provisioning) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after commit

	if _, err := tx.Exec(ctx, `INSERT INTO public.organizations (org_id, name) VALUES ($1, $2)`, p.orgID, p.orgName); err != nil {
		return fmt.Errorf("organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO public.users (user_id, username, password_hash, role, is_active, org_id)
		VALUES ($1, $2, crypt($3, gen_salt('bf')), 'ADMIN'::public.user_role, true, $4)`,
		p.adminUserID, p.adminUser, stressPassword, p.orgID); err != nil {
		return fmt.Errorf("admin user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO public.passkey_credentials (credential_id, user_id, org_id, qx, qy, key_id, role, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'ADMIN', 'PENDING')`,
		p.adminCred, p.adminUserID, p.orgID, p.adminQX[:], p.adminQY[:], p.adminKeyID[:]); err != nil {
		return fmt.Errorf("admin passkey: %w", err)
	}
	for i := range p.operators {
		op := &p.operators[i]
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.users (user_id, username, password_hash, role, is_active, org_id)
			VALUES ($1, $2, crypt($3, gen_salt('bf')), 'OPERATOR'::public.user_role, true, $4)`,
			op.userID, op.username, stressPassword, p.orgID); err != nil {
			return fmt.Errorf("operator user %s: %w", op.username, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.passkey_credentials (credential_id, user_id, org_id, qx, qy, key_id, role, status)
			VALUES ($1, $2, $3, $4, $5, $6, 'OPERATOR', 'PENDING')`,
			op.credID, op.userID, p.orgID, op.qx[:], op.qy[:], op.keyID[:]); err != nil {
			return fmt.Errorf("operator passkey %s: %w", op.username, err)
		}
	}
	return tx.Commit(ctx)
}

// ── org registration and adapter restart ─────────────────────────────────────

// orgFile is the registry-cli org.json format (ledger-adapter/cmd/registry-cli/org.go).
type orgFile struct {
	OrgID string `json:"orgId"`
	Name  string `json:"name"`
	QX    string `json:"qx"`
	QY    string `json:"qy"`
}

func newOrgFile(p *provisioning) orgFile {
	return orgFile{
		OrgID: p.orgID.String(),
		Name:  p.orgName,
		QX:    "0x" + hex.EncodeToString(p.adminQX[:]),
		QY:    "0x" + hex.EncodeToString(p.adminQY[:]),
	}
}

// repoRoot resolves the repository top level: the registry-cli container mounts
// the repo at /work, so --file must be repo-root-relative.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// registerOrg runs `make registry-cli ARGS="register-org --file <rel>"` from the
// repo root, inheriting the environment (COMPOSE_PROJECT_NAME applies) and
// streaming the output.
func registerOrg(root, fileRel string) error {
	cmd := exec.Command("make", "registry-cli", "ARGS=register-org --file "+fileRel)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make registry-cli ARGS=\"register-org --file %s\": %w", fileRel, err)
	}
	return nil
}

// restartAdapter restarts the ledger-adapter container: the owner tx consumed
// one nonce of the shared deployer key, leaving the adapter's local nonce stale
// (the e2e recreates the adapter for the same reason).
func restartAdapter(container string) error {
	out, err := exec.Command("docker", "restart", container).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker restart %s: %s: %w", container, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// waitHealthy polls url until it answers 200 (the adapter /health after restart).
func waitHealthy(ctx context.Context, url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("timeout waiting for %s: %w", url, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// ── WMS HTTP client: login + success envelope, 401 → re-login once ───────────

type wmsClient struct {
	baseURL string
	http    *http.Client
	user    string
	pass    string
	token   string
}

// raw performs one HTTP request with a JSON body and returns status + body.
func (c *wmsClient) raw(ctx context.Context, method, path string, in any) (int, []byte, error) {
	var reader io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// login authenticates and stores the access token (/auth/login returns the raw
// token pair, without the success envelope).
func (c *wmsClient) login(ctx context.Context) error {
	var pair struct {
		AccessToken string `json:"access_token"`
	}
	status, body, err := c.raw(ctx, http.MethodPost, "/auth/login", map[string]string{
		"username": c.user,
		"password": c.pass,
	})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("POST /auth/login: status %d: %s", status, body)
	}
	if err := json.Unmarshal(body, &pair); err != nil {
		return fmt.Errorf("POST /auth/login: %w: %s", err, body)
	}
	if pair.AccessToken == "" {
		return errors.New("POST /auth/login: empty access_token")
	}
	c.token = pair.AccessToken
	return nil
}

// call performs an enveloped WMS request. On 401 it re-logins once and retries
// (the JWT expires on runs longer than the access TTL; the e2e suite refreshes
// its token the same way).
func (c *wmsClient) call(ctx context.Context, method, path string, in, out any) error {
	status, body, err := c.raw(ctx, method, path, in)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		if err := c.login(ctx); err != nil {
			return fmt.Errorf("%s %s: re-login after 401: %w", method, path, err)
		}
		status, body, err = c.raw(ctx, method, path, in)
		if err != nil {
			return err
		}
	}
	if status != http.StatusOK {
		return fmt.Errorf("%s %s: status %d: %s", method, path, status, body)
	}
	var envelope struct {
		Success bool             `json:"success"`
		Data    json.RawMessage  `json:"data"`
		Error   *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("%s %s: %w: %s", method, path, err, body)
	}
	if !envelope.Success || envelope.Error != nil {
		return fmt.Errorf("%s %s: api error: %s", method, path, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}

// waitForOrganization polls GET /passkeys/organization until want holds (2s
// poll, like the e2e waitForOrganization); progress runs on every fetched view
// that is not yet final.
func waitForOrganization(ctx context.Context, c *wmsClient, timeout time.Duration, want func(organizationDTO) bool, progress func(organizationDTO)) (organizationDTO, error) {
	deadline := time.Now().Add(timeout)
	var last organizationDTO
	for {
		var org organizationDTO
		if err := c.call(ctx, http.MethodGet, "/passkeys/organization", nil, &org); err == nil {
			last = org
			if want(org) {
				return org, nil
			}
			if progress != nil {
				progress(org)
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			raw, _ := json.Marshal(last)
			return last, fmt.Errorf("organization did not reach the expected state in %s: %s", timeout, raw)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// countActive counts the given usernames whose passkey is ACTIVE in the view.
func countActive(org organizationDTO, usernames []string) int {
	k := 0
	for _, u := range usernames {
		if m, ok := memberByUsername(org, u); ok && m.Passkey != nil && m.Passkey.Status == "ACTIVE" {
			k++
		}
	}
	return k
}

// submitOperatorKey runs the admin confirmation of one pending operator key:
// draft → softSign(admin key, challenge) → submit expecting state SENT (the
// newSignedOperator flow of tests/e2e/signed_operator_test.go).
func submitOperatorKey(ctx context.Context, client *wmsClient, p *provisioning, op *operator) error {
	var draft keyActionDraftDTO
	if err := client.call(ctx, http.MethodPost, "/passkeys/actions/key-register/draft", map[string]string{
		"target_key_id": "0x" + hex.EncodeToString(op.keyID[:]),
	}, &draft); err != nil {
		return fmt.Errorf("draft: %w", err)
	}
	challengeBytes, err := base64.RawURLEncoding.DecodeString(draft.Challenge)
	if err != nil {
		return fmt.Errorf("challenge: %w", err)
	}
	var challenge [32]byte
	copy(challenge[:], challengeBytes)
	assertion := softSign(p.adminKey, challenge)

	var sent confirmationDTO
	if err := client.call(ctx, http.MethodPost, "/passkeys/actions/"+draft.ActionID+"/submit", map[string]string{
		"credential_id":      base64.RawURLEncoding.EncodeToString(p.adminCred),
		"authenticator_data": base64.RawURLEncoding.EncodeToString(assertion.AuthenticatorData),
		"client_data_json":   base64.RawURLEncoding.EncodeToString(assertion.ClientDataJSON),
		"signature":          base64.RawURLEncoding.EncodeToString(assertion.Signature),
	}, &sent); err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	if sent.State != "SENT" {
		return fmt.Errorf("state %q, want SENT", sent.State)
	}
	return nil
}

// ── credential files for k6 ──────────────────────────────────────────────────

// jwk is the private P-256 JWK of one passkey; x, y and d are big-endian and
// left-padded to exactly 32 bytes (FillBytes), base64url without padding —
// WebCrypto importKey fails on 31-byte coordinates with a leading zero byte.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	D   string `json:"d"`
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func buildJWK(k *ecdsa.PrivateKey) jwk {
	return jwk{
		Kty: "EC",
		Crv: "P-256",
		X:   b64url(k.X.FillBytes(make([]byte, 32))),
		Y:   b64url(k.Y.FillBytes(make([]byte, 32))),
		D:   b64url(k.D.FillBytes(make([]byte, 32))),
	}
}

// keyRecord is one entry of operators.json / admin.json.
type keyRecord struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	CredentialID string `json:"credentialId"`
	KeyID        string `json:"keyId"`
	JWK          jwk    `json:"jwk"`
}

func keyRecordFor(username string, key *ecdsa.PrivateKey, credID []byte, keyID [32]byte) keyRecord {
	return keyRecord{
		Username:     username,
		Password:     stressPassword,
		CredentialID: b64url(credID),
		KeyID:        "0x" + hex.EncodeToString(keyID[:]),
		JWK:          buildJWK(key),
	}
}

// ── CLI ──────────────────────────────────────────────────────────────────────

func main() {
	nOperators := flag.Int("operators", defaultOperators, "number of OPERATOR users to provision")
	outDir := flag.String("out", defaultOut, "output directory (relative to tests/e2e) for operators.json/admin.json")
	flag.Parse()

	if err := run(context.Background(), *nOperators, *outDir); err != nil {
		log.Fatalf("stress-provision: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, n int, outDir string) error {
	dbURL := envOr("DB_URL", defaultDBURL)
	wmsURL := envOr("WMS_URL", defaultWMSURL)
	adapterContainer := envOr("ADAPTER_CONTAINER", defaultAdapterContainer)
	adapterHealthURL := envOr("ADAPTER_HEALTH_URL", defaultAdapterHealthURL)

	// 1. Keys and usernames.
	suffix, err := newSuffix()
	if err != nil {
		return fmt.Errorf("generate suffix: %w", err)
	}
	p, err := newProvisioning(suffix, n)
	if err != nil {
		return fmt.Errorf("generate keys: %w", err)
	}
	log.Printf("generated org %s «%s», admin %s, %d operators", p.orgID, p.orgName, p.adminUser, n)

	// 2. All rows in one transaction.
	if err := insertRows(ctx, dbURL, p); err != nil {
		return fmt.Errorf("insert rows: %w", err)
	}

	// 3. org.json + owner registration via registry-cli.
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return fmt.Errorf("resolve out dir: %w", err)
	}
	if err := os.MkdirAll(absOut, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	root, err := repoRoot()
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}
	orgPath := filepath.Join(absOut, "org.json")
	fileRel, err := filepath.Rel(root, orgPath)
	if err != nil {
		return fmt.Errorf("resolve org.json path: %w", err)
	}
	orgJSON, err := json.MarshalIndent(newOrgFile(p), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal org.json: %w", err)
	}
	if err := os.WriteFile(orgPath, append(orgJSON, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", orgPath, err)
	}
	log.Printf("registering org on chain: make registry-cli ARGS=\"register-org --file %s\" (cwd %s)", fileRel, root)
	if err := registerOrg(root, fileRel); err != nil {
		return fmt.Errorf("register org: %w", err)
	}

	// 4. Fresh adapter nonce, then wait for its health endpoint.
	log.Printf("restarting adapter container %s (stale deployer nonce)", adapterContainer)
	if err := restartAdapter(adapterContainer); err != nil {
		return fmt.Errorf("restart adapter: %w", err)
	}
	if err := waitHealthy(ctx, adapterHealthURL, 90*time.Second); err != nil {
		return fmt.Errorf("adapter health: %w", err)
	}

	// 5. Admin login, then wait until the admin passkey is ACTIVE.
	client := &wmsClient{
		baseURL: wmsURL,
		http:    &http.Client{Timeout: 30 * time.Second},
		user:    p.adminUser,
		pass:    stressPassword,
	}
	if err := client.login(ctx); err != nil {
		return fmt.Errorf("admin login: %w", err)
	}
	_, err = waitForOrganization(ctx, client, 120*time.Second, func(org organizationDTO) bool {
		m, ok := memberByUsername(org, p.adminUser)
		return ok && m.Passkey != nil && m.Passkey.Status == "ACTIVE"
	}, nil)
	if err != nil {
		return fmt.Errorf("wait admin ACTIVE: %w", err)
	}

	// 6. Submit every operator's key-register action sequentially (one client).
	for i := range p.operators {
		if err := submitOperatorKey(ctx, client, p, &p.operators[i]); err != nil {
			return fmt.Errorf("submit operator %d/%d: %w", i+1, n, err)
		}
		log.Printf("submitted %d/%d (%s)", i+1, n, p.operators[i].username)
	}

	// 7. Wait until every operator passkey is ACTIVE on chain.
	usernames := make([]string, n)
	for i := range p.operators {
		usernames[i] = p.operators[i].username
	}
	_, err = waitForOrganization(ctx, client, 300*time.Second, func(org organizationDTO) bool {
		return countActive(org, usernames) == n
	}, func(org organizationDTO) {
		log.Printf("active %d/%d", countActive(org, usernames), n)
	})
	if err != nil {
		return fmt.Errorf("wait operators ACTIVE: %w", err)
	}

	// 8. Credentials for k6.
	adminJSON, err := json.MarshalIndent(keyRecordFor(p.adminUser, p.adminKey, p.adminCred, p.adminKeyID), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal admin.json: %w", err)
	}
	ops := make([]keyRecord, n)
	for i := range p.operators {
		op := &p.operators[i]
		ops[i] = keyRecordFor(op.username, op.key, op.credID, op.keyID)
	}
	opsJSON, err := json.MarshalIndent(ops, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal operators.json: %w", err)
	}
	adminPath := filepath.Join(absOut, "admin.json")
	if err := os.WriteFile(adminPath, append(adminJSON, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", adminPath, err)
	}
	opsPath := filepath.Join(absOut, "operators.json")
	if err := os.WriteFile(opsPath, append(opsJSON, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", opsPath, err)
	}

	fmt.Printf("provisioned org %s: %d operators ACTIVE → %s/operators.json\n", p.orgID, n, absOut)
	return nil
}
