package passkeys

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/ledger"
)

type mockRepo struct {
	getMemberFn             func(ctx context.Context, userID uuid.UUID) (Member, error)
	listOrgMembersFn        func(ctx context.Context, orgID uuid.UUID) ([]Member, error)
	latestPasskeyFn         func(ctx context.Context, userID uuid.UUID) (*domain.Passkey, error)
	latestPasskeysByOrgFn   func(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]domain.Passkey, error)
	insertPasskeyFn         func(ctx context.Context, p domain.Passkey) error
	updatePasskeyStatusFn   func(ctx context.Context, credentialID []byte, status domain.PasskeyStatus) error
	rejectPendingPasskeyFn  func(ctx context.Context, credentialID []byte) (bool, error)
	passkeyByKeyIDFn        func(ctx context.Context, keyID [32]byte) (*domain.Passkey, error)
	passkeyByCredentialIDFn func(ctx context.Context, id []byte) (*domain.Passkey, error)
	insertKeyActionFn       func(ctx context.Context, a KeyAction) error
	keyActionFn             func(ctx context.Context, actionID uuid.UUID) (*KeyAction, error)
	latestConfirmationsFn   func(ctx context.Context, orgID uuid.UUID) (map[string]KeyConfirmation, error)
	submitKeyActionFn       func(ctx context.Context, s KeyActionSubmission) error
}

func (m *mockRepo) GetMember(ctx context.Context, userID uuid.UUID) (Member, error) {
	if m.getMemberFn != nil {
		return m.getMemberFn(ctx, userID)
	}
	panic("GetMember not configured")
}

func (m *mockRepo) ListOrgMembers(ctx context.Context, orgID uuid.UUID) ([]Member, error) {
	if m.listOrgMembersFn != nil {
		return m.listOrgMembersFn(ctx, orgID)
	}
	panic("ListOrgMembers not configured")
}

func (m *mockRepo) LatestPasskey(ctx context.Context, userID uuid.UUID) (*domain.Passkey, error) {
	if m.latestPasskeyFn != nil {
		return m.latestPasskeyFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockRepo) LatestPasskeysByOrg(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]domain.Passkey, error) {
	if m.latestPasskeysByOrgFn != nil {
		return m.latestPasskeysByOrgFn(ctx, orgID)
	}
	panic("LatestPasskeysByOrg not configured")
}

func (m *mockRepo) InsertPasskey(ctx context.Context, p domain.Passkey) error {
	if m.insertPasskeyFn != nil {
		return m.insertPasskeyFn(ctx, p)
	}
	panic("InsertPasskey not configured")
}

func (m *mockRepo) UpdatePasskeyStatus(ctx context.Context, credentialID []byte, status domain.PasskeyStatus) error {
	if m.updatePasskeyStatusFn != nil {
		return m.updatePasskeyStatusFn(ctx, credentialID, status)
	}
	panic("UpdatePasskeyStatus not configured")
}

func (m *mockRepo) RejectPendingPasskey(ctx context.Context, credentialID []byte) (bool, error) {
	if m.rejectPendingPasskeyFn != nil {
		return m.rejectPendingPasskeyFn(ctx, credentialID)
	}
	// Default honours the PENDING condition: only a currently PENDING key can
	// be rejected, and the fake flips its status to REJECTED in place.
	p, err := m.PasskeyByCredentialID(ctx, credentialID)
	if err != nil {
		return false, err
	}
	if p == nil || p.Status != domain.PasskeyStatusPending {
		return false, nil
	}
	p.Status = domain.PasskeyStatusRejected
	return true, nil
}

func (m *mockRepo) PasskeyByKeyID(ctx context.Context, keyID [32]byte) (*domain.Passkey, error) {
	if m.passkeyByKeyIDFn != nil {
		return m.passkeyByKeyIDFn(ctx, keyID)
	}
	return nil, nil
}

func (m *mockRepo) PasskeyByCredentialID(ctx context.Context, id []byte) (*domain.Passkey, error) {
	if m.passkeyByCredentialIDFn != nil {
		return m.passkeyByCredentialIDFn(ctx, id)
	}
	return nil, nil
}

func (m *mockRepo) InsertKeyAction(ctx context.Context, a KeyAction) error {
	if m.insertKeyActionFn != nil {
		return m.insertKeyActionFn(ctx, a)
	}
	panic("InsertKeyAction not configured")
}

func (m *mockRepo) KeyAction(ctx context.Context, actionID uuid.UUID) (*KeyAction, error) {
	if m.keyActionFn != nil {
		return m.keyActionFn(ctx, actionID)
	}
	return nil, nil
}

func (m *mockRepo) LatestConfirmations(ctx context.Context, orgID uuid.UUID) (map[string]KeyConfirmation, error) {
	if m.latestConfirmationsFn != nil {
		return m.latestConfirmationsFn(ctx, orgID)
	}
	return nil, nil
}

func (m *mockRepo) SubmitKeyAction(ctx context.Context, s KeyActionSubmission) error {
	if m.submitKeyActionFn != nil {
		return m.submitKeyActionFn(ctx, s)
	}
	panic("SubmitKeyAction not configured")
}

type fakeChain struct {
	key   ledger.RegistryKey
	err   error
	calls int
}

func (f *fakeChain) GetRegistryKey(_ context.Context, _ string) (ledger.RegistryKey, error) {
	f.calls++
	return f.key, f.err
}

var (
	orgA       = uuid.MustParse("0192aaaa-0000-7000-8000-00000000000a")
	orgB       = uuid.MustParse("0192bbbb-0000-7000-8000-00000000000b")
	adminA     = uuid.MustParse("0192aaaa-0000-7000-8000-0000000000a1")
	operatorA  = uuid.MustParse("0192aaaa-0000-7000-8000-0000000000a2")
	adminB     = uuid.MustParse("0192bbbb-0000-7000-8000-0000000000b1")
	noOrgAdmin = uuid.MustParse("0192cccc-0000-7000-8000-0000000000c1")
)

func memberFixture(id, org uuid.UUID, username string, role domain.UserRole, name string) Member {
	m := Member{UserID: id, Username: username, Role: role, OrgName: name}
	if org != uuid.Nil {
		o := org
		m.OrgID = &o
	}
	return m
}

func newTestService(t *testing.T, repo repository, chain KeyStatusGetter) *Service {
	t.Helper()
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          "localhost",
		RPDisplayName: "test",
		RPOrigins:     []string{"http://localhost:5173"},
	})
	if err != nil {
		t.Fatalf("webauthn.New: %v", err)
	}
	return NewService(repo, wa, chain)
}

func membersRepo(members map[uuid.UUID]Member) *mockRepo {
	return &mockRepo{
		getMemberFn: func(_ context.Context, userID uuid.UUID) (Member, error) {
			m, ok := members[userID]
			if !ok {
				return Member{}, errors.New("user not found")
			}
			return m, nil
		},
	}
}

func livePasskey(userID, org uuid.UUID, status domain.PasskeyStatus) *domain.Passkey {
	qx := mustHex32ForTest(vectorQX)
	qy := mustHex32ForTest(vectorQY)
	return &domain.Passkey{
		CredentialID: []byte("cred-1"),
		UserID:       userID,
		OrgID:        org,
		QX:           qx,
		QY:           qy,
		KeyID:        keyID(qx, qy),
		Role:         domain.UserRoleAdmin,
		Status:       status,
		CreatedAt:    time.Now(),
	}
}

func mustHex32ForTest(s string) [32]byte {
	b, _ := hex.DecodeString(s)
	var out [32]byte
	copy(out[:], b)
	return out
}

func TestBeginRegistration_NoOrganization(t *testing.T) {
	members := map[uuid.UUID]Member{noOrgAdmin: memberFixture(noOrgAdmin, uuid.Nil, "lonely", domain.UserRoleAdmin, "")}
	svc := newTestService(t, membersRepo(members), nil)

	if _, err := svc.BeginRegistration(context.Background(), noOrgAdmin); !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("err = %v, want ErrNoOrganization", err)
	}
}

func TestBeginRegistration_LiveKeyExists(t *testing.T) {
	members := map[uuid.UUID]Member{adminA: memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A")}
	repo := membersRepo(members)
	repo.latestPasskeyFn = func(context.Context, uuid.UUID) (*domain.Passkey, error) {
		return livePasskey(adminA, orgA, domain.PasskeyStatusPending), nil
	}
	svc := newTestService(t, repo, nil)

	if _, err := svc.BeginRegistration(context.Background(), adminA); !errors.Is(err, ErrPasskeyExists) {
		t.Fatalf("err = %v, want ErrPasskeyExists", err)
	}
}

func TestBeginRegistration_Success(t *testing.T) {
	members := map[uuid.UUID]Member{operatorA: memberFixture(operatorA, orgA, "operator", domain.UserRoleOperator, "Org A")}
	svc := newTestService(t, membersRepo(members), nil)

	creation, err := svc.BeginRegistration(context.Background(), operatorA)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	raw, err := json.Marshal(creation)
	if err != nil {
		t.Fatalf("marshal creation: %v", err)
	}
	var doc struct {
		PublicKey struct {
			PubKeyCredParams []struct {
				Type string `json:"type"`
				Alg  int    `json:"alg"`
			} `json:"pubKeyCredParams"`
			AuthenticatorSelection struct {
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal creation: %v (%s)", err, raw)
	}
	params := doc.PublicKey.PubKeyCredParams
	if len(params) != 1 || params[0].Alg != int(webauthncose.AlgES256) {
		t.Fatalf("pubKeyCredParams = %+v, want one ES256 entry", params)
	}
	if doc.PublicKey.AuthenticatorSelection.UserVerification != "required" {
		t.Fatalf("userVerification = %q, want required", doc.PublicKey.AuthenticatorSelection.UserVerification)
	}
}

func TestFinishRegistration_WithoutBeginIsInvalid(t *testing.T) {
	members := map[uuid.UUID]Member{adminA: memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A")}
	svc := newTestService(t, membersRepo(members), nil)

	_, err := svc.FinishRegistration(context.Background(), adminA, strings.NewReader("{}"))
	if !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("err = %v, want ErrInvalidRegistration", err)
	}
}

func TestFinishRegistration_GarbageBodyIsInvalid(t *testing.T) {
	members := map[uuid.UUID]Member{adminA: memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A")}
	svc := newTestService(t, membersRepo(members), nil)
	if _, err := svc.BeginRegistration(context.Background(), adminA); err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	_, err := svc.FinishRegistration(context.Background(), adminA, strings.NewReader("not json"))
	if !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("err = %v, want ErrInvalidRegistration", err)
	}
}

func TestFinishRegistration_ExpiredSessionIsInvalid(t *testing.T) {
	members := map[uuid.UUID]Member{adminA: memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A")}
	svc := newTestService(t, membersRepo(members), nil)
	creation, err := svc.BeginRegistration(context.Background(), adminA)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	svc.now = func() time.Time { return time.Now().Add(registrationSessionTTL + time.Minute) }

	body := buildAttestation(t, []byte(creation.Response.Challenge), 0x45, newP256Key(t))
	_, err = svc.FinishRegistration(context.Background(), adminA, bytes.NewReader(body))
	if !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("err = %v, want ErrInvalidRegistration", err)
	}
}

func TestFinishRegistration_ValidNoneAttestation(t *testing.T) {
	members := map[uuid.UUID]Member{adminA: memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A")}
	priv := newP256Key(t)

	var inserted []domain.Passkey
	repo := membersRepo(members)
	repo.insertPasskeyFn = func(_ context.Context, p domain.Passkey) error {
		inserted = append(inserted, p)
		return nil
	}
	svc := newTestService(t, repo, nil)
	creation, err := svc.BeginRegistration(context.Background(), adminA)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	body := buildAttestation(t, []byte(creation.Response.Challenge), 0x45, priv)
	p, err := svc.FinishRegistration(context.Background(), adminA, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("FinishRegistration: %v", err)
	}

	wantX := priv.PublicKey.X.FillBytes(make([]byte, 32))
	wantY := priv.PublicKey.Y.FillBytes(make([]byte, 32))
	if !bytes.Equal(p.QX[:], wantX) || !bytes.Equal(p.QY[:], wantY) {
		t.Fatalf("coordinates mismatch")
	}
	if p.KeyID != keyID(p.QX, p.QY) {
		t.Fatalf("keyID is not keccak256(qx||qy)")
	}
	if p.Status != domain.PasskeyStatusPending || p.Role != domain.UserRoleAdmin || p.UserID != adminA || p.OrgID != orgA {
		t.Fatalf("unexpected passkey: %+v", p)
	}
	if len(inserted) != 1 || inserted[0].KeyID != p.KeyID {
		t.Fatalf("InsertPasskey calls = %d, want 1 with the same key", len(inserted))
	}
}

func TestFinishRegistration_WithoutUserVerificationIsRejected(t *testing.T) {
	members := map[uuid.UUID]Member{adminA: memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A")}
	inserts := 0
	repo := membersRepo(members)
	repo.insertPasskeyFn = func(context.Context, domain.Passkey) error {
		inserts++
		return nil
	}
	svc := newTestService(t, repo, nil)
	creation, err := svc.BeginRegistration(context.Background(), adminA)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	body := buildAttestation(t, []byte(creation.Response.Challenge), 0x41, newP256Key(t))
	_, err = svc.FinishRegistration(context.Background(), adminA, bytes.NewReader(body))
	if !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("err = %v, want ErrInvalidRegistration", err)
	}
	if inserts != 0 {
		t.Fatalf("InsertPasskey called %d times, want 0", inserts)
	}
}

func TestOrganization_OnlyOwnOrganization(t *testing.T) {
	members := map[uuid.UUID]Member{
		adminA:     memberFixture(adminA, orgA, "a-admin", domain.UserRoleAdmin, "Org A"),
		operatorA:  memberFixture(operatorA, orgA, "a-operator", domain.UserRoleOperator, "Org A"),
		adminB:     memberFixture(adminB, orgB, "b-admin", domain.UserRoleAdmin, "Org B"),
		noOrgAdmin: memberFixture(noOrgAdmin, uuid.Nil, "lonely", domain.UserRoleAdmin, ""),
	}
	byOrg := map[uuid.UUID][]Member{}
	for _, m := range members {
		if m.OrgID != nil {
			byOrg[*m.OrgID] = append(byOrg[*m.OrgID], m)
		}
	}

	var listedOrgs, keyOrgs []uuid.UUID
	repo := membersRepo(members)
	repo.listOrgMembersFn = func(_ context.Context, orgID uuid.UUID) ([]Member, error) {
		listedOrgs = append(listedOrgs, orgID)
		return byOrg[orgID], nil
	}
	repo.latestPasskeysByOrgFn = func(_ context.Context, orgID uuid.UUID) (map[uuid.UUID]domain.Passkey, error) {
		keyOrgs = append(keyOrgs, orgID)
		return map[uuid.UUID]domain.Passkey{}, nil
	}
	svc := newTestService(t, repo, nil)

	overviewA, err := svc.Organization(context.Background(), adminA)
	if err != nil {
		t.Fatalf("admin A: %v", err)
	}
	if len(overviewA.Members) != 2 {
		t.Fatalf("admin A sees %d members, want 2", len(overviewA.Members))
	}
	for _, m := range overviewA.Members {
		if m.UserID == adminB {
			t.Fatal("admin A sees org B member")
		}
	}
	overviewB, err := svc.Organization(context.Background(), adminB)
	if err != nil {
		t.Fatalf("admin B: %v", err)
	}
	if len(overviewB.Members) != 1 || overviewB.Members[0].UserID != adminB {
		t.Fatalf("admin B members = %+v", overviewB.Members)
	}
	if listedOrgs[0] != orgA || listedOrgs[1] != orgB || keyOrgs[0] != orgA || keyOrgs[1] != orgB {
		t.Fatalf("repo queried orgs %v / %v", listedOrgs, keyOrgs)
	}

	if _, err := svc.Organization(context.Background(), operatorA); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator err = %v, want ErrForbidden", err)
	}
	if _, err := svc.Organization(context.Background(), noOrgAdmin); !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("no-org admin err = %v, want ErrNoOrganization", err)
	}
}

func TestSyncStatus(t *testing.T) {
	cases := []struct {
		name       string
		status     domain.PasskeyStatus
		chain      *fakeChain
		wantStatus domain.PasskeyStatus
		wantUpdate bool
		wantCalls  int
	}{
		{"pending becomes active", domain.PasskeyStatusPending, &fakeChain{key: ledger.RegistryKey{Found: true, Active: true}}, domain.PasskeyStatusActive, true, 1},
		{"active becomes revoked", domain.PasskeyStatusActive, &fakeChain{key: ledger.RegistryKey{Found: true, Active: false}}, domain.PasskeyStatusRevoked, true, 1},
		{"chain error keeps status", domain.PasskeyStatusPending, &fakeChain{err: errors.New("502")}, domain.PasskeyStatusPending, false, 1},
		{"not found keeps status", domain.PasskeyStatusPending, &fakeChain{key: ledger.RegistryKey{Found: false}}, domain.PasskeyStatusPending, false, 1},
		{"revoked is not queried", domain.PasskeyStatusRevoked, &fakeChain{key: ledger.RegistryKey{Found: true, Active: true}}, domain.PasskeyStatusRevoked, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := membersRepo(nil)
			repo.latestPasskeyFn = func(context.Context, uuid.UUID) (*domain.Passkey, error) {
				return livePasskey(adminA, orgA, tc.status), nil
			}
			var updated domain.PasskeyStatus
			updates := 0
			repo.updatePasskeyStatusFn = func(_ context.Context, _ []byte, status domain.PasskeyStatus) error {
				updates++
				updated = status
				return nil
			}
			svc := newTestService(t, repo, tc.chain)

			p, err := svc.MyPasskey(context.Background(), adminA)
			if err != nil {
				t.Fatalf("MyPasskey: %v", err)
			}
			if p.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", p.Status, tc.wantStatus)
			}
			if (updates == 1) != tc.wantUpdate || (tc.wantUpdate && updated != tc.wantStatus) {
				t.Fatalf("updates = %d (status %s), wantUpdate = %v", updates, updated, tc.wantUpdate)
			}
			if tc.chain.calls != tc.wantCalls {
				t.Fatalf("adapter calls = %d, want %d", tc.chain.calls, tc.wantCalls)
			}
		})
	}
}

func TestAdminKeyExport(t *testing.T) {
	members := map[uuid.UUID]Member{
		adminA:    memberFixture(adminA, orgA, "a-admin", domain.UserRoleAdmin, "Org A"),
		operatorA: memberFixture(operatorA, orgA, "a-operator", domain.UserRoleOperator, "Org A"),
	}

	t.Run("exact JSON", func(t *testing.T) {
		repo := membersRepo(members)
		repo.latestPasskeyFn = func(context.Context, uuid.UUID) (*domain.Passkey, error) {
			return livePasskey(adminA, orgA, domain.PasskeyStatusPending), nil
		}
		export, err := newTestService(t, repo, nil).AdminKeyExport(context.Background(), adminA)
		if err != nil {
			t.Fatalf("AdminKeyExport: %v", err)
		}
		raw, err := json.Marshal(export)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		want := `{"orgId":"` + orgA.String() + `","name":"Org A","qx":"0x` + vectorQX + `","qy":"0x` + vectorQY + `"}`
		if string(raw) != want {
			t.Fatalf("export JSON = %s\nwant %s", raw, want)
		}
	})

	t.Run("no key", func(t *testing.T) {
		svc := newTestService(t, membersRepo(members), nil)
		if _, err := svc.AdminKeyExport(context.Background(), adminA); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("err = %v, want ErrPasskeyNotFound", err)
		}
	})

	t.Run("revoked key", func(t *testing.T) {
		repo := membersRepo(members)
		repo.latestPasskeyFn = func(context.Context, uuid.UUID) (*domain.Passkey, error) {
			return livePasskey(adminA, orgA, domain.PasskeyStatusRevoked), nil
		}
		svc := newTestService(t, repo, nil)
		if _, err := svc.AdminKeyExport(context.Background(), adminA); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("err = %v, want ErrPasskeyNotFound", err)
		}
	})

	t.Run("operator forbidden", func(t *testing.T) {
		svc := newTestService(t, membersRepo(members), nil)
		if _, err := svc.AdminKeyExport(context.Background(), operatorA); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}

func newP256Key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv
}

// buildAttestation assembles a "none" attestation registration response the way a
// browser would produce it, signed by no one (none attestation has no statement).
func buildAttestation(t *testing.T, challenge []byte, flags byte, priv *ecdsa.PrivateKey) []byte {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString

	x := priv.PublicKey.X.FillBytes(make([]byte, 32))
	y := priv.PublicKey.Y.FillBytes(make([]byte, 32))
	cose := coseKey(t, int64(webauthncose.AlgES256), 1, x, y)

	credID := make([]byte, 16)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("random credential id: %v", err)
	}
	rpHash := sha256.Sum256([]byte("localhost"))

	authData := append([]byte{}, rpHash[:]...)
	authData = append(authData, flags, 0, 0, 0, 0)
	authData = append(authData, make([]byte, 16)...)
	authData = append(authData, byte(len(credID)>>8), byte(len(credID)))
	authData = append(authData, credID...)
	authData = append(authData, cose...)

	attObj, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authData})
	if err != nil {
		t.Fatalf("marshal attestation: %v", err)
	}
	clientData := []byte(`{"type":"webauthn.create","challenge":"` + enc(challenge) + `","origin":"http://localhost:5173"}`)

	body, err := json.Marshal(map[string]any{
		"id":    enc(credID),
		"rawId": enc(credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    enc(clientData),
			"attestationObject": enc(attObj),
		},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return body
}
