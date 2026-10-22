package assembly

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
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
	"wms/internal/signedops"
)

// stampRecord — один вызов StampTaskUnload, зафиксированный моком.
type stampRecord struct {
	ActionID      uuid.UUID
	ProductIDs    []uuid.UUID
	OperatorID    uuid.UUID
	DestinationID uuid.UUID
	OccurredAt    time.Time
}

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
	point := signedactiontest.Coordinates(key)
	return &domain.Passkey{
		CredentialID: []byte("cred-operator"),
		Key:          point,
		KeyID:        signedaction.KeyID(point),
		Status:       domain.PasskeyStatusActive,
	}, key
}

// newPickOperation builds a one-part pick operation over the given items with the
// basket's product→order mapping baked into the details hash.
func newPickOperation(t *testing.T, bufferBinID, operatorID uuid.UUID, orderByProduct map[uuid.UUID]uuid.UUID, items []uuid.UUID) (*signedops.Operation, *domain.Passkey, *ecdsa.PrivateKey) {
	t.Helper()
	signer, key := signedSigner()
	details, err := json.Marshal(pickDetails{ShippingBufferBinID: bufferBinID, OccurredAtMs: 1760781720000})
	if err != nil {
		t.Fatalf("marshal details: %v", err)
	}
	op, err := signedops.NewOperation(signedaction.TypePick, operatorID, signer, details, items,
		func(salt [32]byte, partItems []uuid.UUID) [32]byte {
			orders := make([]uuid.UUID, 0, len(partItems))
			for _, id := range partItems {
				if orderID, ok := orderByProduct[id]; ok {
					orders = append(orders, orderID)
				}
			}
			return signedaction.PickDetailsHash(salt, bufferBinID, orders, operatorID, 1760781720000)
		})
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	return op, signer, key
}

// signParts signs every part of the operation with the test key.
func signParts(t *testing.T, op *signedops.Operation, key *ecdsa.PrivateKey, credentialID []byte) map[uuid.UUID]signedaction.Assertion {
	t.Helper()
	assertions := map[uuid.UUID]signedaction.Assertion{}
	for _, p := range op.Parts {
		a := signedactiontest.Sign(key, p.Challenge)
		a.CredentialID = credentialID
		assertions[p.ActionID] = a
	}
	return assertions
}

func serveAssembly(t *testing.T, svc *Service, method, path, body string, userID uuid.UUID, role domain.UserRole) *httptest.ResponseRecorder {
	t.Helper()
	r := mux.NewRouter()
	NewHandler(svc).RegisterRoutes(r.PathPrefix("/assembly").Subrouter())
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), userID, role))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
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
