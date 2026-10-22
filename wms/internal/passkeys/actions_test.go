package passkeys

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
	"wms/internal/signedaction"
	"wms/internal/signedaction/signedactiontest"
)

func operatorPasskey(status domain.PasskeyStatus) *domain.Passkey {
	p := livePasskey(operatorA, orgA, status)
	p.Role = domain.UserRoleOperator
	p.CredentialID = []byte("cred-operator")
	return p
}

func int16Ptr(v int16) *int16 { return &v }

// actionRepo builds a repo fixture with an ACTIVE admin signer and a PENDING
// operator target; tests override single fields on the returned passkeys.
func actionRepo(t *testing.T) (*mockRepo, *domain.Passkey, *domain.Passkey) {
	t.Helper()
	signer := livePasskey(adminA, orgA, domain.PasskeyStatusActive)
	target := operatorPasskey(domain.PasskeyStatusPending)
	repo := membersRepo(map[uuid.UUID]Member{
		adminA:    memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A"),
		operatorA: memberFixture(operatorA, orgA, "operator", domain.UserRoleOperator, "Org A"),
	})
	repo.latestPasskeyFn = func(context.Context, uuid.UUID) (*domain.Passkey, error) { return signer, nil }
	repo.passkeyByKeyIDFn = func(_ context.Context, keyID [32]byte) (*domain.Passkey, error) {
		if keyID == target.KeyID {
			return target, nil
		}
		return nil, nil
	}
	repo.passkeyByCredentialIDFn = func(_ context.Context, id []byte) (*domain.Passkey, error) {
		switch {
		case bytes.Equal(id, signer.CredentialID):
			return signer, nil
		case bytes.Equal(id, target.CredentialID):
			return target, nil
		}
		return nil, nil
	}
	return repo, signer, target
}

type actionFixture struct {
	svc         *Service
	repo        *mockRepo
	signer      *domain.Passkey
	target      *domain.Passkey
	stored      *KeyAction
	submissions []KeyActionSubmission
	signerKey   *ecdsa.PrivateKey
}

// newActionFixture wires the happy-path flow: active signer key with real
// coordinates, pending operator target, in-memory key_actions storage.
func newActionFixture(t *testing.T) *actionFixture {
	t.Helper()
	repo, signer, target := actionRepo(t)
	f := &actionFixture{repo: repo, signer: signer, target: target}
	f.signerKey = signedactiontest.NewKey()
	key := signedactiontest.Coordinates(f.signerKey)
	signer.Key, signer.KeyID = key, signedaction.KeyID(key)
	repo.insertKeyActionFn = func(_ context.Context, a KeyAction) error {
		cp := a
		f.stored = &cp
		return nil
	}
	repo.keyActionFn = func(_ context.Context, id uuid.UUID) (*KeyAction, error) {
		if f.stored != nil && id == f.stored.ActionID {
			return f.stored, nil
		}
		return nil, nil
	}
	repo.submitKeyActionFn = func(_ context.Context, s KeyActionSubmission) error {
		f.submissions = append(f.submissions, s)
		return nil
	}
	f.svc = newTestService(t, repo, nil)
	return f
}

func (f *actionFixture) draft(t *testing.T) *KeyAction {
	t.Helper()
	d, err := f.svc.DraftKeyRegister(context.Background(), adminA, "0x"+hex.EncodeToString(f.target.KeyID[:]))
	if err != nil {
		t.Fatalf("DraftKeyRegister: %v", err)
	}
	return &d.Action
}

func (f *actionFixture) assertion(t *testing.T, action *KeyAction) signedaction.Assertion {
	t.Helper()
	a := signedactiontest.Sign(f.signerKey, action.Challenge)
	a.CredentialID = f.signer.CredentialID
	return a
}

func submitRequestBody(t *testing.T, a signedaction.Assertion) string {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString
	raw, err := json.Marshal(signedaction.AssertionRequest{
		CredentialID:      enc(a.CredentialID),
		AuthenticatorData: enc(a.AuthenticatorData),
		ClientDataJSON:    enc(a.ClientDataJSON),
		Signature:         enc(a.Signature),
	})
	if err != nil {
		t.Fatalf("marshal submit body: %v", err)
	}
	return string(raw)
}

func TestDraftKeyRegister_Success(t *testing.T) {
	f := newActionFixture(t)

	draft, err := f.svc.DraftKeyRegister(context.Background(), adminA, "0x"+hex.EncodeToString(f.target.KeyID[:]))
	if err != nil {
		t.Fatalf("DraftKeyRegister: %v", err)
	}

	want := signedaction.Challenge(draft.Action.ActionID, signedaction.TypeKeyRegister, signedaction.NoItems, signedaction.KeyDetailsHash(f.target.Key))
	if draft.Action.Challenge != want {
		t.Errorf("challenge = %x, want %x", draft.Action.Challenge, want)
	}
	if draft.Action.ActionType != signedaction.TypeKeyRegister {
		t.Errorf("actionType = %d, want 5", draft.Action.ActionType)
	}
	if draft.Action.ActionID.Version() != 7 {
		t.Errorf("actionID version = %d, want 7", draft.Action.ActionID.Version())
	}
	if draft.Subject != f.target.Key {
		t.Errorf("subject key mismatch")
	}
	if draft.DetailsHash != signedaction.KeyDetailsHash(f.target.Key) {
		t.Errorf("detailsHash mismatch")
	}
	if draft.SignerKeyID != f.signer.KeyID {
		t.Errorf("signerKeyID mismatch")
	}
	if draft.RPID != "localhost" {
		t.Errorf("rpID = %q, want localhost", draft.RPID)
	}
	if f.stored == nil {
		t.Fatalf("InsertKeyAction not called")
	}
	if f.stored.Challenge != want {
		t.Errorf("stored challenge = %x, want %x", f.stored.Challenge, want)
	}
	if !bytes.Equal(f.stored.TargetCredentialID, f.target.CredentialID) || !bytes.Equal(f.stored.SignerCredentialID, f.signer.CredentialID) {
		t.Errorf("stored credentials mismatch")
	}
	if f.stored.OrgID != orgA || f.stored.CreatedBy != adminA {
		t.Errorf("stored org/creator mismatch")
	}
}

func TestDraftKeyRegister_Errors(t *testing.T) {
	targetKeyID := func(target *domain.Passkey) string {
		return "0x" + hex.EncodeToString(target.KeyID[:])
	}

	t.Run("signer key not active", func(t *testing.T) {
		f := newActionFixture(t)
		f.signer.Status = domain.PasskeyStatusPending
		if _, err := f.svc.DraftKeyRegister(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrSignerKeyNotActive) {
			t.Fatalf("err = %v, want ErrSignerKeyNotActive", err)
		}
	})
	t.Run("target of another org", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.OrgID = orgB
		if _, err := f.svc.DraftKeyRegister(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("err = %v, want ErrPasskeyNotFound", err)
		}
	})
	t.Run("target is not an operator key", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.Role = domain.UserRoleAdmin
		if _, err := f.svc.DraftKeyRegister(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("err = %v, want ErrPasskeyNotFound", err)
		}
	})
	t.Run("target not pending", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.Status = domain.PasskeyStatusActive
		if _, err := f.svc.DraftKeyRegister(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrTargetNotPending) {
			t.Fatalf("err = %v, want ErrTargetNotPending", err)
		}
	})
	t.Run("action in progress", func(t *testing.T) {
		f := newActionFixture(t)
		f.repo.latestConfirmationsFn = func(context.Context, uuid.UUID) (map[string]KeyConfirmation, error) {
			return map[string]KeyConfirmation{
				string(f.target.CredentialID): {State: signedaction.ConfirmationSent},
			}, nil
		}
		if _, err := f.svc.DraftKeyRegister(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrActionInProgress) {
			t.Fatalf("err = %v, want ErrActionInProgress", err)
		}
	})
	t.Run("invalid key id", func(t *testing.T) {
		f := newActionFixture(t)
		if _, err := f.svc.DraftKeyRegister(context.Background(), adminA, "0xzz"); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
}

func TestDraftKeyRevoke_Success(t *testing.T) {
	f := newActionFixture(t)
	f.target.Status = domain.PasskeyStatusActive

	draft, err := f.svc.DraftKeyRevoke(context.Background(), adminA, "0x"+hex.EncodeToString(f.target.KeyID[:]))
	if err != nil {
		t.Fatalf("DraftKeyRevoke: %v", err)
	}

	want := signedaction.Challenge(draft.Action.ActionID, signedaction.TypeKeyRevoke, signedaction.NoItems, signedaction.KeyDetailsHash(f.target.Key))
	if draft.Action.ActionType != signedaction.TypeKeyRevoke {
		t.Errorf("actionType = %d, want 6", draft.Action.ActionType)
	}
	if draft.Action.Challenge != want {
		t.Errorf("challenge = %x, want %x", draft.Action.Challenge, want)
	}
	if draft.Action.ActionID.Version() != 7 {
		t.Errorf("actionID version = %d, want 7", draft.Action.ActionID.Version())
	}
	if draft.Subject != f.target.Key {
		t.Errorf("subject key mismatch")
	}
	if draft.DetailsHash != signedaction.KeyDetailsHash(f.target.Key) {
		t.Errorf("detailsHash mismatch")
	}
	if f.stored == nil || f.stored.ActionType != signedaction.TypeKeyRevoke {
		t.Errorf("stored action = %+v, want type 6", f.stored)
	}
	if !bytes.Equal(f.stored.TargetCredentialID, f.target.CredentialID) || !bytes.Equal(f.stored.SignerCredentialID, f.signer.CredentialID) {
		t.Errorf("stored credentials mismatch")
	}
}

func TestDraftKeyRevoke_Errors(t *testing.T) {
	targetKeyID := func(target *domain.Passkey) string {
		return "0x" + hex.EncodeToString(target.KeyID[:])
	}

	t.Run("target of another org", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.Status = domain.PasskeyStatusActive
		f.target.OrgID = orgB
		if _, err := f.svc.DraftKeyRevoke(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("err = %v, want ErrPasskeyNotFound", err)
		}
	})
	t.Run("target is not an operator key", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.Status = domain.PasskeyStatusActive
		f.target.Role = domain.UserRoleAdmin
		if _, err := f.svc.DraftKeyRevoke(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("err = %v, want ErrPasskeyNotFound", err)
		}
	})
	t.Run("target not active", func(t *testing.T) {
		f := newActionFixture(t)
		if _, err := f.svc.DraftKeyRevoke(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrTargetNotActive) {
			t.Fatalf("err = %v, want ErrTargetNotActive", err)
		}
	})
	t.Run("confirmation in flight", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.Status = domain.PasskeyStatusActive
		f.repo.latestConfirmationsFn = func(context.Context, uuid.UUID) (map[string]KeyConfirmation, error) {
			return map[string]KeyConfirmation{
				string(f.target.CredentialID): {State: signedaction.ConfirmationSent},
			}, nil
		}
		if _, err := f.svc.DraftKeyRevoke(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrActionInProgress) {
			t.Fatalf("err = %v, want ErrActionInProgress", err)
		}
	})
	t.Run("applied register does not block revoke", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.Status = domain.PasskeyStatusActive
		f.repo.latestConfirmationsFn = func(context.Context, uuid.UUID) (map[string]KeyConfirmation, error) {
			return map[string]KeyConfirmation{
				string(f.target.CredentialID): {State: signedaction.ConfirmationApplied, ActionType: signedaction.TypeKeyRegister},
			}, nil
		}
		if _, err := f.svc.DraftKeyRevoke(context.Background(), adminA, targetKeyID(f.target)); err != nil {
			t.Fatalf("DraftKeyRevoke: %v", err)
		}
	})
	t.Run("invalid key id", func(t *testing.T) {
		f := newActionFixture(t)
		if _, err := f.svc.DraftKeyRevoke(context.Background(), adminA, "0xzz"); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
}

func TestSubmitKeyAction_RevokeEnvelope(t *testing.T) {
	f := newActionFixture(t)
	f.target.Status = domain.PasskeyStatusActive
	draft, err := f.svc.DraftKeyRevoke(context.Background(), adminA, "0x"+hex.EncodeToString(f.target.KeyID[:]))
	if err != nil {
		t.Fatalf("DraftKeyRevoke: %v", err)
	}

	confirmation, err := f.svc.SubmitKeyAction(context.Background(), adminA, draft.Action.ActionID, f.assertion(t, &draft.Action))
	if err != nil {
		t.Fatalf("SubmitKeyAction: %v", err)
	}
	if confirmation.ActionType != signedaction.TypeKeyRevoke || confirmation.State != signedaction.ConfirmationSent {
		t.Fatalf("confirmation = %+v, want type 6 SENT", confirmation)
	}
	if f.submissions[0].ActionType != signedaction.TypeKeyRevoke {
		t.Errorf("submission actionType = %d, want 6", f.submissions[0].ActionType)
	}
	var doc struct {
		ActionType int `json:"actionType"`
	}
	if err := json.Unmarshal(f.submissions[0].Payload, &doc); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if doc.ActionType != 6 {
		t.Errorf("envelope actionType = %d, want 6", doc.ActionType)
	}
}

func TestRejectKey_Success(t *testing.T) {
	f := newActionFixture(t)

	if err := f.svc.RejectKey(context.Background(), adminA, "0x"+hex.EncodeToString(f.target.KeyID[:])); err != nil {
		t.Fatalf("RejectKey: %v", err)
	}
	if f.target.Status != domain.PasskeyStatusRejected {
		t.Fatalf("target status = %s, want REJECTED", f.target.Status)
	}
}

func TestRejectKey_Errors(t *testing.T) {
	targetKeyID := func(target *domain.Passkey) string {
		return "0x" + hex.EncodeToString(target.KeyID[:])
	}

	t.Run("unknown key", func(t *testing.T) {
		f := newActionFixture(t)
		if err := f.svc.RejectKey(context.Background(), adminA, "0x"+strings.Repeat("ab", 32)); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("err = %v, want ErrPasskeyNotFound", err)
		}
	})
	t.Run("target of another org", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.OrgID = orgB
		if err := f.svc.RejectKey(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("err = %v, want ErrPasskeyNotFound", err)
		}
	})
	t.Run("target not pending", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.Status = domain.PasskeyStatusActive
		if err := f.svc.RejectKey(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrTargetNotPending) {
			t.Fatalf("err = %v, want ErrTargetNotPending", err)
		}
	})
	t.Run("action in progress", func(t *testing.T) {
		f := newActionFixture(t)
		f.repo.latestConfirmationsFn = func(context.Context, uuid.UUID) (map[string]KeyConfirmation, error) {
			return map[string]KeyConfirmation{
				string(f.target.CredentialID): {State: signedaction.ConfirmationSent},
			}, nil
		}
		if err := f.svc.RejectKey(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrActionInProgress) {
			t.Fatalf("err = %v, want ErrActionInProgress", err)
		}
	})
	t.Run("invalid key id", func(t *testing.T) {
		f := newActionFixture(t)
		if err := f.svc.RejectKey(context.Background(), adminA, "0xzz"); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
}

// TestRejectKey_RepoErrorMapping checks that sentinel errors from the
// transactional reject pass through Service.RejectKey unwrapped, so the HTTP
// layer can map them by errors.Is.
func TestRejectKey_RepoErrorMapping(t *testing.T) {
	targetKeyID := func(target *domain.Passkey) string {
		return "0x" + hex.EncodeToString(target.KeyID[:])
	}

	t.Run("repo action in progress", func(t *testing.T) {
		f := newActionFixture(t)
		f.repo.rejectPendingPasskeyFn = func(context.Context, []byte) error { return ErrActionInProgress }
		if err := f.svc.RejectKey(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrActionInProgress) {
			t.Fatalf("err = %v, want ErrActionInProgress", err)
		}
	})
	t.Run("repo target not pending", func(t *testing.T) {
		f := newActionFixture(t)
		f.repo.rejectPendingPasskeyFn = func(context.Context, []byte) error { return ErrTargetNotPending }
		if err := f.svc.RejectKey(context.Background(), adminA, targetKeyID(f.target)); !errors.Is(err, ErrTargetNotPending) {
			t.Fatalf("err = %v, want ErrTargetNotPending", err)
		}
	})
	t.Run("other repo error is wrapped", func(t *testing.T) {
		f := newActionFixture(t)
		f.repo.rejectPendingPasskeyFn = func(context.Context, []byte) error { return errors.New("db down") }
		err := f.svc.RejectKey(context.Background(), adminA, targetKeyID(f.target))
		if err == nil || errors.Is(err, ErrTargetNotPending) || errors.Is(err, ErrActionInProgress) {
			t.Fatalf("err = %v, want a wrapped non-sentinel error", err)
		}
	})
}

func TestHandler_RejectKeyForbiddenForOperator(t *testing.T) {
	f := newActionFixture(t)

	rec := serveAsBody(t, f.svc, http.MethodPost, "/passkeys/organization/keys/reject",
		strings.NewReader(`{"target_key_id":"0x00"}`), operatorA, domain.UserRoleOperator)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reject status = %d, want 403", rec.Code)
	}
}

func TestHandler_DraftKeyRevokeRoute(t *testing.T) {
	f := newActionFixture(t)
	f.target.Status = domain.PasskeyStatusActive
	body := `{"target_key_id":"0x` + hex.EncodeToString(f.target.KeyID[:]) + `"}`

	rec := serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/key-revoke/draft",
		strings.NewReader(body), operatorA, domain.UserRoleOperator)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator status = %d, want 403", rec.Code)
	}

	rec = serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/key-revoke/draft",
		strings.NewReader(body), adminA, domain.UserRoleAdmin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var draft struct {
		ActionID   string `json:"action_id"`
		ActionType int    `json:"action_type"`
		Challenge  string `json:"challenge"`
	}
	if err := json.Unmarshal(dataEnvelope(t, rec), &draft); err != nil {
		t.Fatalf("unmarshal draft: %v", err)
	}
	if draft.ActionType != 6 {
		t.Errorf("action_type = %d, want 6", draft.ActionType)
	}
	if draft.Challenge != base64.RawURLEncoding.EncodeToString(f.stored.Challenge[:]) {
		t.Errorf("challenge does not match the stored action")
	}
}

func TestHandler_DraftKeyRevokeErrorMapping(t *testing.T) {
	t.Run("TARGET_NOT_ACTIVE", func(t *testing.T) {
		f := newActionFixture(t)
		rec := serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/key-revoke/draft",
			strings.NewReader(`{"target_key_id":"0x`+hex.EncodeToString(f.target.KeyID[:])+`"}`), adminA, domain.UserRoleAdmin)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "TARGET_NOT_ACTIVE" {
			t.Fatalf("code = %q, want TARGET_NOT_ACTIVE", code)
		}
	})
	t.Run("PASSKEY_NOT_FOUND", func(t *testing.T) {
		f := newActionFixture(t)
		f.target.Status = domain.PasskeyStatusActive
		f.target.Role = domain.UserRoleAdmin
		rec := serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/key-revoke/draft",
			strings.NewReader(`{"target_key_id":"0x`+hex.EncodeToString(f.target.KeyID[:])+`"}`), adminA, domain.UserRoleAdmin)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if code := errorCode(t, rec); code != "PASSKEY_NOT_FOUND" {
			t.Fatalf("code = %q, want PASSKEY_NOT_FOUND", code)
		}
	})
}

func TestSubmitKeyAction_Success(t *testing.T) {
	f := newActionFixture(t)
	action := f.draft(t)
	assertion := f.assertion(t, action)

	confirmation, err := f.svc.SubmitKeyAction(context.Background(), adminA, action.ActionID, assertion)
	if err != nil {
		t.Fatalf("SubmitKeyAction: %v", err)
	}
	if confirmation.ActionID != action.ActionID || confirmation.State != signedaction.ConfirmationSent {
		t.Fatalf("confirmation = %+v, want SENT of %s", confirmation, action.ActionID)
	}
	if len(f.submissions) != 1 {
		t.Fatalf("SubmitKeyAction calls = %d, want 1", len(f.submissions))
	}
	sub := f.submissions[0]
	if sub.ActionID != action.ActionID {
		t.Errorf("submission actionID = %s, want %s", sub.ActionID, action.ActionID)
	}
	if sub.PayloadHash != hex.EncodeToString(action.Challenge[:]) {
		t.Errorf("payloadHash = %s, want hex of challenge", sub.PayloadHash)
	}

	var doc struct {
		Version    int      `json:"version"`
		ActionID   string   `json:"actionId"`
		ActionType int      `json:"actionType"`
		ItemIDs    []string `json:"itemIds"`
		SubjectKey struct {
			QX string `json:"qx"`
			QY string `json:"qy"`
		} `json:"subjectKey"`
		KeyID    string `json:"keyId"`
		WebAuthn struct {
			AuthenticatorData string `json:"authenticatorData"`
			ClientDataJSON    string `json:"clientDataJSON"`
			ChallengeIndex    int    `json:"challengeIndex"`
			TypeIndex         int    `json:"typeIndex"`
			R                 string `json:"r"`
			S                 string `json:"s"`
		} `json:"webauthn"`
	}
	if err := json.Unmarshal(sub.Payload, &doc); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, sub.Payload)
	}
	if doc.Version != 1 || doc.ActionID != action.ActionID.String() || doc.ActionType != 5 {
		t.Errorf("envelope header = %+v", doc)
	}
	if doc.ItemIDs == nil || len(doc.ItemIDs) != 0 {
		t.Errorf("itemIds = %#v, want []", doc.ItemIDs)
	}
	if !strings.Contains(string(sub.Payload), `"itemIds":[]`) {
		t.Errorf("itemIds does not marshal as []: %s", sub.Payload)
	}
	if doc.SubjectKey.QX != "0x"+hex.EncodeToString(f.target.Key.X[:]) || doc.SubjectKey.QY != "0x"+hex.EncodeToString(f.target.Key.Y[:]) {
		t.Errorf("subjectKey = %v, want target coordinates", doc.SubjectKey)
	}
	if doc.KeyID != "0x"+hex.EncodeToString(f.signer.KeyID[:]) {
		t.Errorf("keyId = %s, want signer key id", doc.KeyID)
	}
	if len(doc.WebAuthn.R) != 66 || len(doc.WebAuthn.S) != 66 {
		t.Errorf("r/s hex length = %d/%d, want 66", len(doc.WebAuthn.R), len(doc.WebAuthn.S))
	}
	if doc.WebAuthn.ChallengeIndex != 23 || doc.WebAuthn.TypeIndex != 1 {
		t.Errorf("indexes = (%d, %d), want (23, 1)", doc.WebAuthn.ChallengeIndex, doc.WebAuthn.TypeIndex)
	}
	if doc.WebAuthn.ClientDataJSON != string(assertion.ClientDataJSON) {
		t.Errorf("clientDataJSON mismatch")
	}
	s, ok := new(big.Int).SetString(strings.TrimPrefix(doc.WebAuthn.S, "0x"), 16)
	if !ok {
		t.Fatalf("unparseable s: %s", doc.WebAuthn.S)
	}
	if s.Cmp(new(big.Int).Rsh(elliptic.P256().Params().N, 1)) > 0 {
		t.Errorf("s %s is not low-s", s)
	}
}

func TestSubmitKeyAction_Errors(t *testing.T) {
	t.Run("signature by another key", func(t *testing.T) {
		f := newActionFixture(t)
		action := f.draft(t)
		assertion := signedactiontest.Sign(signedactiontest.NewKey(), action.Challenge)
		assertion.CredentialID = f.signer.CredentialID

		if _, err := f.svc.SubmitKeyAction(context.Background(), adminA, action.ActionID, assertion); !errors.Is(err, ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
		if len(f.submissions) != 0 {
			t.Fatalf("SubmitKeyAction called %d times, want 0", len(f.submissions))
		}
	})
	t.Run("credential mismatch", func(t *testing.T) {
		f := newActionFixture(t)
		action := f.draft(t)
		assertion := f.assertion(t, action)
		assertion.CredentialID = []byte("some-other-credential")

		if _, err := f.svc.SubmitKeyAction(context.Background(), adminA, action.ActionID, assertion); !errors.Is(err, ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
	})
	t.Run("already submitted", func(t *testing.T) {
		f := newActionFixture(t)
		action := f.draft(t)
		at := time.Now()
		f.stored.SubmittedAt = &at

		if _, err := f.svc.SubmitKeyAction(context.Background(), adminA, action.ActionID, f.assertion(t, action)); !errors.Is(err, ErrActionAlreadySubmitted) {
			t.Fatalf("err = %v, want ErrActionAlreadySubmitted", err)
		}
	})
	t.Run("unknown action", func(t *testing.T) {
		f := newActionFixture(t)
		if _, err := f.svc.SubmitKeyAction(context.Background(), adminA, uuid.New(), signedaction.Assertion{}); !errors.Is(err, ErrActionNotFound) {
			t.Fatalf("err = %v, want ErrActionNotFound", err)
		}
	})
	t.Run("draft of another user", func(t *testing.T) {
		f := newActionFixture(t)
		action := f.draft(t)
		f.stored.CreatedBy = operatorA

		if _, err := f.svc.SubmitKeyAction(context.Background(), adminA, action.ActionID, f.assertion(t, action)); !errors.Is(err, ErrActionNotFound) {
			t.Fatalf("err = %v, want ErrActionNotFound", err)
		}
	})
}

func TestOrganization_FillsConfirmation(t *testing.T) {
	target := operatorPasskey(domain.PasskeyStatusActive)
	repo := membersRepo(map[uuid.UUID]Member{
		adminA:    memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A"),
		operatorA: memberFixture(operatorA, orgA, "operator", domain.UserRoleOperator, "Org A"),
	})
	repo.listOrgMembersFn = func(context.Context, uuid.UUID) ([]Member, error) {
		return []Member{
			memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A"),
			memberFixture(operatorA, orgA, "operator", domain.UserRoleOperator, "Org A"),
		}, nil
	}
	repo.latestPasskeysByOrgFn = func(context.Context, uuid.UUID) (map[uuid.UUID]domain.Passkey, error) {
		return map[uuid.UUID]domain.Passkey{
			adminA:    *livePasskey(adminA, orgA, domain.PasskeyStatusActive),
			operatorA: *target,
		}, nil
	}
	actionID := uuid.MustParse("01929c5e-6f00-7000-8000-000000001001")
	repo.latestConfirmationsFn = func(context.Context, uuid.UUID) (map[string]KeyConfirmation, error) {
		return map[string]KeyConfirmation{
			string(target.CredentialID): {ActionID: actionID, State: signedaction.ConfirmationRejected, RejectReason: int16Ptr(6)},
		}, nil
	}
	svc := newTestService(t, repo, nil)

	overview, err := svc.Organization(context.Background(), adminA)
	if err != nil {
		t.Fatalf("Organization: %v", err)
	}
	byUser := map[uuid.UUID]OrgMember{}
	for _, m := range overview.Members {
		byUser[m.UserID] = m
	}
	operator := byUser[operatorA]
	if operator.Confirmation == nil || operator.Confirmation.State != signedaction.ConfirmationRejected || operator.Confirmation.RejectReason == nil || *operator.Confirmation.RejectReason != 6 || operator.Confirmation.ActionID != actionID {
		t.Fatalf("operator confirmation = %+v, want REJECTED reason 6", operator.Confirmation)
	}
	if byUser[adminA].Confirmation != nil {
		t.Fatalf("admin confirmation = %+v, want nil", byUser[adminA].Confirmation)
	}
}

func serveAsBody(t *testing.T, svc *Service, method, path string, body io.Reader, userID uuid.UUID, role domain.UserRole) *httptest.ResponseRecorder {
	t.Helper()
	r := mux.NewRouter()
	NewHandler(svc).RegisterRoutes(r.PathPrefix("/passkeys").Subrouter())

	req := httptest.NewRequest(method, path, body)
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), userID, role))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func dataEnvelope(t *testing.T, rec *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var env struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, rec.Body.String())
	}
	if env.Error != nil {
		t.Fatalf("unexpected error envelope: %s", rec.Body.String())
	}
	return env.Data
}

func TestHandler_DraftAndSubmitForbiddenForOperator(t *testing.T) {
	f := newActionFixture(t)
	action := f.draft(t)

	rec := serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/key-register/draft",
		strings.NewReader(`{"target_key_id":"0x00"}`), operatorA, domain.UserRoleOperator)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("draft status = %d, want 403", rec.Code)
	}
	rec = serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/"+action.ActionID.String()+"/submit",
		strings.NewReader(`{}`), operatorA, domain.UserRoleOperator)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("submit status = %d, want 403", rec.Code)
	}
}

func TestHandler_DraftResponseShape(t *testing.T) {
	f := newActionFixture(t)
	body := `{"target_key_id":"0x` + hex.EncodeToString(f.target.KeyID[:]) + `"}`
	rec := serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/key-register/draft",
		strings.NewReader(body), adminA, domain.UserRoleAdmin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	raw := dataEnvelope(t, rec)
	if !strings.Contains(string(raw), `"item_ids":[]`) {
		t.Errorf("item_ids does not marshal as []: %s", raw)
	}

	var draft struct {
		ActionID   string `json:"action_id"`
		ActionType int    `json:"action_type"`
		SubjectKey struct {
			QX string `json:"qx"`
			QY string `json:"qy"`
		} `json:"subject_key"`
		DetailsHash      string `json:"details_hash"`
		SignerKeyID      string `json:"signer_key_id"`
		Challenge        string `json:"challenge"`
		RPID             string `json:"rp_id"`
		AllowCredentials []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"allow_credentials"`
		UserVerification string `json:"user_verification"`
		Timeout          int    `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatalf("unmarshal draft: %v (%s)", err, raw)
	}
	if draft.ActionID != f.stored.ActionID.String() || draft.ActionType != 5 {
		t.Errorf("action = %s/%d, want %s/5", draft.ActionID, draft.ActionType, f.stored.ActionID)
	}
	if draft.SubjectKey.QX != "0x"+hex.EncodeToString(f.target.Key.X[:]) || draft.SubjectKey.QY != "0x"+hex.EncodeToString(f.target.Key.Y[:]) {
		t.Errorf("subject_key = %v", draft.SubjectKey)
	}
	detailsHash := signedaction.KeyDetailsHash(f.target.Key)
	if draft.DetailsHash != "0x"+hex.EncodeToString(detailsHash[:]) {
		t.Errorf("details_hash = %s", draft.DetailsHash)
	}
	if draft.SignerKeyID != "0x"+hex.EncodeToString(f.signer.KeyID[:]) {
		t.Errorf("signer_key_id = %s", draft.SignerKeyID)
	}
	challenge, err := base64.RawURLEncoding.DecodeString(draft.Challenge)
	if err != nil || len(challenge) != 32 {
		t.Errorf("challenge = %q (%v), want base64url of 32 bytes", draft.Challenge, err)
	}
	if draft.Challenge != base64.RawURLEncoding.EncodeToString(f.stored.Challenge[:]) {
		t.Errorf("challenge does not match the stored action")
	}
	if draft.RPID != "localhost" || draft.UserVerification != "required" || draft.Timeout != 60000 {
		t.Errorf("rp_id/user_verification/timeout = %q/%q/%d", draft.RPID, draft.UserVerification, draft.Timeout)
	}
	if len(draft.AllowCredentials) != 1 || draft.AllowCredentials[0].Type != "public-key" ||
		draft.AllowCredentials[0].ID != base64.RawURLEncoding.EncodeToString(f.signer.CredentialID) {
		t.Errorf("allow_credentials = %+v", draft.AllowCredentials)
	}
}

func TestHandler_SubmitSuccess(t *testing.T) {
	f := newActionFixture(t)
	action := f.draft(t)
	body := submitRequestBody(t, f.assertion(t, action))

	rec := serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/"+action.ActionID.String()+"/submit",
		strings.NewReader(body), adminA, domain.UserRoleAdmin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var confirmation confirmationView
	if err := json.Unmarshal(dataEnvelope(t, rec), &confirmation); err != nil {
		t.Fatalf("unmarshal confirmation: %v", err)
	}
	if confirmation.ActionID != action.ActionID.String() || confirmation.State != "SENT" || confirmation.RejectReason != nil {
		t.Fatalf("confirmation = %+v, want SENT with null reason", confirmation)
	}
}

func TestHandler_SubmitBadBase64IsInvalidRequest(t *testing.T) {
	f := newActionFixture(t)
	action := f.draft(t)
	assertion := f.assertion(t, action)
	body := submitRequestBody(t, assertion)
	body = strings.Replace(body, base64.RawURLEncoding.EncodeToString(assertion.Signature), "!!!not-base64", 1)

	rec := serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/"+action.ActionID.String()+"/submit",
		strings.NewReader(body), adminA, domain.UserRoleAdmin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := errorCode(t, rec); code != "INVALID_REQUEST" {
		t.Fatalf("code = %q, want INVALID_REQUEST", code)
	}
}

func TestHandler_NewErrorMapping(t *testing.T) {
	draftPath := "/passkeys/actions/key-register/draft"
	draftBody := func(f *actionFixture) string {
		return `{"target_key_id":"0x` + hex.EncodeToString(f.target.KeyID[:]) + `"}`
	}

	cases := []struct {
		name       string
		setup      func(f *actionFixture)
		run        func(f *actionFixture) *httptest.ResponseRecorder
		wantCode   string
		wantStatus int
	}{
		{
			name:  "SIGNER_KEY_NOT_ACTIVE",
			setup: func(f *actionFixture) { f.signer.Status = domain.PasskeyStatusPending },
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				return serveAsBody(t, f.svc, http.MethodPost, draftPath, strings.NewReader(draftBody(f)), adminA, domain.UserRoleAdmin)
			},
			wantCode: "SIGNER_KEY_NOT_ACTIVE", wantStatus: http.StatusConflict,
		},
		{
			name: "REGISTRY_UNAVAILABLE",
			setup: func(f *actionFixture) { f.svc.chain = &fakeChain{err: errors.New("502")} },
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				return serveAsBody(t, f.svc, http.MethodPost, draftPath, strings.NewReader(draftBody(f)), adminA, domain.UserRoleAdmin)
			},
			wantCode: "REGISTRY_UNAVAILABLE", wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:  "TARGET_NOT_PENDING",
			setup: func(f *actionFixture) { f.target.Status = domain.PasskeyStatusActive },
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				return serveAsBody(t, f.svc, http.MethodPost, draftPath, strings.NewReader(draftBody(f)), adminA, domain.UserRoleAdmin)
			},
			wantCode: "TARGET_NOT_PENDING", wantStatus: http.StatusConflict,
		},
		{
			name: "ACTION_IN_PROGRESS",
			setup: func(f *actionFixture) {
				f.repo.latestConfirmationsFn = func(context.Context, uuid.UUID) (map[string]KeyConfirmation, error) {
					return map[string]KeyConfirmation{string(f.target.CredentialID): {State: signedaction.ConfirmationSent}}, nil
				}
			},
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				return serveAsBody(t, f.svc, http.MethodPost, draftPath, strings.NewReader(draftBody(f)), adminA, domain.UserRoleAdmin)
			},
			wantCode: "ACTION_IN_PROGRESS", wantStatus: http.StatusConflict,
		},
		{
			name:  "INVALID_REQUEST on bad key id",
			setup: func(*actionFixture) {},
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				return serveAsBody(t, f.svc, http.MethodPost, draftPath, strings.NewReader(`{"target_key_id":"nothex"}`), adminA, domain.UserRoleAdmin)
			},
			wantCode: "INVALID_REQUEST", wantStatus: http.StatusBadRequest,
		},
		{
			name:  "ACTION_NOT_FOUND",
			setup: func(*actionFixture) {},
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				action := f.draft(t)
				return serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/"+uuid.New().String()+"/submit",
					strings.NewReader(submitRequestBody(t, f.assertion(t, action))), adminA, domain.UserRoleAdmin)
			},
			wantCode: "ACTION_NOT_FOUND", wantStatus: http.StatusNotFound,
		},
		{
			name: "ACTION_ALREADY_SUBMITTED",
			setup: func(f *actionFixture) {
				if f.stored == nil {
					f.draft(t)
				}
				at := time.Now()
				f.stored.SubmittedAt = &at
			},
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				return serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/"+f.stored.ActionID.String()+"/submit",
					strings.NewReader(submitRequestBody(t, f.assertion(t, f.stored))), adminA, domain.UserRoleAdmin)
			},
			wantCode: "ACTION_ALREADY_SUBMITTED", wantStatus: http.StatusConflict,
		},
		{
			name:  "INVALID_ASSERTION",
			setup: func(*actionFixture) {},
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				action := f.draft(t)
				assertion := signedactiontest.Sign(signedactiontest.NewKey(), action.Challenge)
				assertion.CredentialID = f.signer.CredentialID
				return serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/"+action.ActionID.String()+"/submit",
					strings.NewReader(submitRequestBody(t, assertion)), adminA, domain.UserRoleAdmin)
			},
			wantCode: "INVALID_ASSERTION", wantStatus: http.StatusBadRequest,
		},
		{
			name:  "INVALID_REQUEST on bad actionID",
			setup: func(*actionFixture) {},
			run: func(f *actionFixture) *httptest.ResponseRecorder {
				return serveAsBody(t, f.svc, http.MethodPost, "/passkeys/actions/not-a-uuid/submit",
					strings.NewReader(`{}`), adminA, domain.UserRoleAdmin)
			},
			wantCode: "INVALID_REQUEST", wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newActionFixture(t)
			tc.setup(f)
			rec := tc.run(f)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if code := errorCode(t, rec); code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}
