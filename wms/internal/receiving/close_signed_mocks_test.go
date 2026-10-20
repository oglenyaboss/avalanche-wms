package receiving

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
	"wms/internal/signedaction"
	"wms/internal/signedaction/signedactiontest"
	"wms/internal/signedops"
)

type mockOperationStore struct {
	inserted   *signedops.Operation
	insertErr  error
	operations map[uuid.UUID]*signedops.Operation
}

func (m *mockOperationStore) InsertOperation(_ context.Context, op *signedops.Operation) error {
	if m.insertErr != nil {
		return m.insertErr
	}
	m.inserted = op
	return nil
}

func (m *mockOperationStore) Operation(_ context.Context, id uuid.UUID) (*signedops.Operation, error) {
	return m.operations[id], nil
}

type mockSignerSource struct {
	signer    *domain.Passkey
	signerErr error
	rpID      string
	origins   []string
}

func (m *mockSignerSource) ActiveSigner(_ context.Context, _ uuid.UUID) (*domain.Passkey, error) {
	if m.signerErr != nil {
		return nil, m.signerErr
	}
	return m.signer, nil
}

func (m *mockSignerSource) RelyingParty() (string, []string) {
	return m.rpID, m.origins
}

// signedSigner returns an operator passkey whose key pair signs real assertions.
func signedSigner() (*domain.Passkey, *ecdsa.PrivateKey) {
	key := signedactiontest.NewKey()
	qx, qy := signedactiontest.Coordinates(key)
	return &domain.Passkey{
		CredentialID: []byte("cred-operator"),
		QX:           qx,
		QY:           qy,
		KeyID:        signedaction.KeyID(qx, qy),
		Status:       domain.PasskeyStatusActive,
	}, key
}

func newSignedOperation(t *testing.T, cargoplaceID, operatorID uuid.UUID, items []uuid.UUID) (*signedops.Operation, *domain.Passkey, *ecdsa.PrivateKey) {
	t.Helper()
	signer, key := signedSigner()
	details, err := json.Marshal(receiveDetails{CargoplaceID: cargoplaceID, OccurredAtMs: 1760781600000})
	if err != nil {
		t.Fatalf("marshal details: %v", err)
	}
	op, err := signedops.NewOperation(signedaction.TypeReceive, operatorID, signer, details, items,
		func(salt [32]byte, _ []uuid.UUID) [32]byte {
			return signedaction.ReceiveDetailsHash(salt, cargoplaceID, operatorID, 1760781600000)
		})
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	return op, signer, key
}

func closeService(repo *mockReceivingRepo, ops *mockOperationStore, signer *mockSignerSource) *Service {
	return NewService(repo, ops, signer)
}

func serveClose(t *testing.T, svc *Service, method, path, body string, userID uuid.UUID, role domain.UserRole) *httptest.ResponseRecorder {
	t.Helper()
	r := mux.NewRouter()
	NewHandler(svc).RegisterRoutes(r.PathPrefix("/receiving").Subrouter())
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), userID, role))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
