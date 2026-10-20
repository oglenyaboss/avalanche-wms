package audit

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"

	"wms/internal/signedaction"
)

type hexBytes []byte

func (h *hexBytes) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return err
	}
	*h = v
	return nil
}

type vectorProduct struct {
	ProductID string   `json:"productId"`
	ItemID    hexBytes `json:"itemId"`
}

type vectorAction struct {
	ActionID   string `json:"actionId"`
	ActionType int    `json:"actionType"`
	Details    struct {
		Salt         hexBytes `json:"salt"`
		StorageBinID string   `json:"storageBinId"`
		OperatorID   string   `json:"operatorId"`
		OccurredAtMs uint64   `json:"occurredAtMs"`
	} `json:"details"`
	DetailsHash hexBytes   `json:"detailsHash"`
	ItemIDs     []hexBytes `json:"itemIds"`
	KeyID       hexBytes   `json:"keyId"`
	WebAuthn    struct {
		AuthenticatorData hexBytes `json:"authenticatorData"`
		ChallengeIndex    int      `json:"challengeIndex"`
		ClientDataJSONHex hexBytes `json:"clientDataJSONHex"`
		R                 hexBytes `json:"r"`
		S                 hexBytes `json:"s"`
		TypeIndex         int      `json:"typeIndex"`
	} `json:"webauthn"`
}

type vectorsFile struct {
	OrgID  string `json:"orgId"`
	Signer struct {
		QX hexBytes `json:"qx"`
		QY hexBytes `json:"qy"`
	} `json:"signer"`
	Products []vectorProduct `json:"products"`
	Actions  []vectorAction  `json:"actions"`
}

func loadVectors(t *testing.T) vectorsFile {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/signed-actions/vectors.v1.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v vectorsFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return v
}

// fixture is the real signed PutAway action from the vectors, wired into fakes.
type fixture struct {
	vectors  vectorsFile
	action   vectorAction
	actionID uuid.UUID
	dbAction Action
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	v := loadVectors(t)
	idx := slices.IndexFunc(v.Actions, func(a vectorAction) bool { return a.ActionType == 2 })
	if idx < 0 {
		t.Fatal("no putAway action in vectors")
	}
	a := v.Actions[idx]
	id, err := uuid.Parse(a.ActionID)
	if err != nil {
		t.Fatalf("parse actionId: %v", err)
	}
	operatorID, err := uuid.Parse(a.Details.OperatorID)
	if err != nil {
		t.Fatalf("parse operatorId: %v", err)
	}
	orgID, err := uuid.Parse(v.OrgID)
	if err != nil {
		t.Fatalf("parse orgId: %v", err)
	}
	itemToProduct := make(map[string]uuid.UUID, len(v.Products))
	for _, p := range v.Products {
		productID, err := uuid.Parse(p.ProductID)
		if err != nil {
			t.Fatalf("parse productId: %v", err)
		}
		itemToProduct[hex.EncodeToString(p.ItemID)] = productID
	}
	itemIDs := make([]uuid.UUID, 0, len(a.ItemIDs))
	for _, it := range a.ItemIDs {
		productID, ok := itemToProduct[hex.EncodeToString(it)]
		if !ok {
			t.Fatalf("no product for itemId %x", it)
		}
		itemIDs = append(itemIDs, productID)
	}
	return fixture{
		vectors:  v,
		action:   a,
		actionID: id,
		dbAction: Action{
			ActionID:    id,
			OperationID: uuid.Must(uuid.NewV7()),
			PartIndex:   0,
			ActionType:  2,
			ItemIDs:     itemIDs,
			Salt:        [32]byte(a.Details.Salt),
			SubmittedAt: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC),
			OperatorID:  operatorID,
			Operator:    "alice",
			OrgID:       orgID,
			OrgName:     "Демо-склад",
			KeyID:       [32]byte(a.KeyID),
			ChainStatus: "COMMITTED",
		},
	}
}

func (f fixture) details() Details {
	binID, _ := uuid.Parse(f.action.Details.StorageBinID)
	operatorID, _ := uuid.Parse(f.action.Details.OperatorID)
	return Details{
		ActionType:   2,
		Salt:         [32]byte(f.action.Details.Salt),
		StorageBinID: binID,
		OperatorID:   operatorID,
		OccurredAtMs: f.action.Details.OccurredAtMs,
	}
}

func (f fixture) chainAction() ChainAction {
	orgID, _ := uuid.Parse(f.vectors.OrgID)
	return ChainAction{
		ActionID:    f.actionID,
		KeyID:       [32]byte(f.action.KeyID),
		OrgID:       orgID,
		ActionType:  2,
		DetailsHash: [32]byte(f.action.DetailsHash),
		ItemCount:   uint64(len(f.action.ItemIDs)),
		BlockNumber: 42,
		BlockTime:   time.Date(2026, 10, 20, 10, 5, 0, 0, time.UTC),
		TxHash:      common.HexToHash("0xabc1"),
	}
}

func (f fixture) onChainAuth() OnChainAuth {
	itemIDs := make([]*big.Int, 0, len(f.action.ItemIDs))
	for _, it := range f.action.ItemIDs {
		itemIDs = append(itemIDs, new(big.Int).SetBytes(it))
	}
	return OnChainAuth{
		ItemIDs:     itemIDs,
		DetailsHash: [32]byte(f.action.DetailsHash),
		KeyID:       [32]byte(f.action.KeyID),
		ActionType:  2,
		WebAuthn: signedaction.WebAuthn{
			AuthenticatorData: f.action.WebAuthn.AuthenticatorData,
			ClientDataJSON:    string(f.action.WebAuthn.ClientDataJSONHex),
			ChallengeIndex:    f.action.WebAuthn.ChallengeIndex,
			TypeIndex:         f.action.WebAuthn.TypeIndex,
			R:                 [32]byte(f.action.WebAuthn.R),
			S:                 [32]byte(f.action.WebAuthn.S),
		},
	}
}

// productOf returns the first product of the fixture action.
func (f fixture) productOf(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(f.vectors.Products[0].ProductID)
	if err != nil {
		t.Fatalf("parse productId: %v", err)
	}
	return id
}

type fakeStore struct {
	byProduct map[uuid.UUID][]Action
	byID      map[uuid.UUID]*Action
	all       []Action
	details   func(a Action) (Details, error)
}

func (f *fakeStore) ActionsForProduct(ctx context.Context, productID uuid.UUID) ([]Action, error) {
	return f.byProduct[productID], nil
}

func (f *fakeStore) Action(ctx context.Context, actionID uuid.UUID) (*Action, error) {
	return f.byID[actionID], nil
}

func (f *fakeStore) AllActions(ctx context.Context) ([]Action, error) {
	return f.all, nil
}

func (f *fakeStore) Details(ctx context.Context, a Action) (Details, error) {
	return f.details(a)
}

type fakeLedger struct {
	itemActions map[uuid.UUID][]uuid.UUID
	items       map[uuid.UUID][][32]byte
	applied     map[uuid.UUID]ChainAction
	rejected    map[uuid.UUID]ChainRejection
	keys        map[[32]byte]Key
	auths       map[uuid.UUID]OnChainAuth
}

func (f *fakeLedger) ActionIDsForItem(ctx context.Context, productID uuid.UUID) ([]uuid.UUID, error) {
	return f.itemActions[productID], nil
}

func (f *fakeLedger) ItemsOfAction(ctx context.Context, actionID uuid.UUID) ([][32]byte, error) {
	return f.items[actionID], nil
}

func (f *fakeLedger) Applied(ctx context.Context, actionIDs []uuid.UUID) (map[uuid.UUID]ChainAction, error) {
	return cloneMap(f.applied), nil
}

func (f *fakeLedger) Rejected(ctx context.Context, actionIDs []uuid.UUID) (map[uuid.UUID]ChainRejection, error) {
	return cloneMap(f.rejected), nil
}

func (f *fakeLedger) Key(ctx context.Context, keyID [32]byte) (Key, error) {
	return f.keys[keyID], nil
}

func (f *fakeLedger) Auth(ctx context.Context, tx common.Hash, actionID uuid.UUID) (OnChainAuth, error) {
	return f.auths[actionID], nil
}

func cloneMap[M ~map[K]V, K comparable, V any](m M) M {
	out := make(M, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// consistentFakes wires the fakes so every check of the fixture action passes.
func consistentFakes(f fixture, product uuid.UUID) (*fakeStore, *fakeLedger) {
	items := make([][32]byte, 0, len(f.action.ItemIDs))
	for _, it := range f.action.ItemIDs {
		items = append(items, [32]byte(it))
	}
	signer := Key{QX: [32]byte(f.vectors.Signer.QX), QY: [32]byte(f.vectors.Signer.QY)}
	s := &fakeStore{
		byProduct: map[uuid.UUID][]Action{product: {f.dbAction}},
		byID:      map[uuid.UUID]*Action{f.actionID: &f.dbAction},
		details:   func(a Action) (Details, error) { return f.details(), nil },
	}
	l := &fakeLedger{
		itemActions: map[uuid.UUID][]uuid.UUID{product: {f.actionID}},
		items:       map[uuid.UUID][][32]byte{f.actionID: items},
		applied:     map[uuid.UUID]ChainAction{f.actionID: f.chainAction()},
		rejected:    map[uuid.UUID]ChainRejection{},
		keys:        map[[32]byte]Key{[32]byte(f.action.KeyID): signer},
		auths:       map[uuid.UUID]OnChainAuth{f.actionID: f.onChainAuth()},
	}
	return s, l
}

func findFinding(findings []Finding, kind Kind) *Finding {
	for i := range findings {
		if findings[i].Kind == kind {
			return &findings[i]
		}
	}
	return nil
}

func TestAudit_Product_Consistent(t *testing.T) {
	f := newFixture(t)
	product := f.productOf(t)
	s, l := consistentFakes(f, product)
	report, err := NewAuditor(s, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if len(report.Stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(report.Stages))
	}
	stage := report.Stages[0]
	if stage.Status != StatusApplied {
		t.Errorf("status = %s, want APPLIED", stage.Status)
	}
	if stage.DetailsHash != CheckOK || stage.Items != CheckOK || stage.Signer != CheckOK || stage.Signature != CheckOK {
		t.Errorf("checks = %s/%s/%s/%s, want OK/OK/OK/OK", stage.DetailsHash, stage.Items, stage.Signer, stage.Signature)
	}
	if len(report.Findings()) != 0 {
		t.Errorf("findings = %v, want none", report.Findings())
	}
	if stage.BlockNumber != 42 || stage.BlockTime == nil || stage.TxHash != common.HexToHash("0xabc1").Hex() {
		t.Errorf("chain fields = %d %v %s", stage.BlockNumber, stage.BlockTime, stage.TxHash)
	}
	if stage.Operator != "alice" || stage.OrgName != "Демо-склад" || stage.OperatorID != mustParseUUID(t, f.action.Details.OperatorID) {
		t.Errorf("db fields = %s %s %s", stage.Operator, stage.OrgName, stage.OperatorID)
	}
	if stage.KeyID != hexKey([32]byte(f.action.KeyID)) {
		t.Errorf("keyId = %s, want %s", stage.KeyID, hexKey([32]byte(f.action.KeyID)))
	}
}

func TestAudit_Product_DetailsHashMismatch(t *testing.T) {
	f := newFixture(t)
	product := f.productOf(t)
	s, l := consistentFakes(f, product)
	s.details = func(a Action) (Details, error) {
		d := f.details()
		d.StorageBinID = mustParseUUID(t, "01929c5e-6f00-7000-8000-00000000dead")
		return d, nil
	}
	report, err := NewAuditor(s, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if report.Stages[0].DetailsHash != CheckMismatch {
		t.Errorf("detailsHash = %s, want MISMATCH", report.Stages[0].DetailsHash)
	}
	if findFinding(report.Findings(), KindDetailsHash) == nil {
		t.Errorf("no %s finding in %v", KindDetailsHash, report.Findings())
	}
}

func TestAudit_Product_DetailsRowMissing(t *testing.T) {
	f := newFixture(t)
	product := f.productOf(t)
	s, l := consistentFakes(f, product)
	s.details = func(a Action) (Details, error) { return Details{}, ErrDomainRowMissing }
	report, err := NewAuditor(s, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if report.Stages[0].DetailsHash != CheckMismatch {
		t.Errorf("detailsHash = %s, want MISMATCH", report.Stages[0].DetailsHash)
	}
	if finding := findFinding(report.Findings(), KindDetailsHash); finding == nil || !strings.Contains(finding.Detail, ErrDomainRowMissing.Error()) {
		t.Errorf("findings = %v, want %s with row-missing detail", report.Findings(), KindDetailsHash)
	}
}

func TestAudit_Product_MissingInDB(t *testing.T) {
	f := newFixture(t)
	product := f.productOf(t)
	s, l := consistentFakes(f, product)
	s.byProduct = map[uuid.UUID][]Action{product: {}}
	report, err := NewAuditor(s, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	stage := report.Stages[0]
	if stage.Status != StatusMissingInDB {
		t.Errorf("status = %s, want MISSING_IN_DB", stage.Status)
	}
	if fd := findFinding(report.Findings(), KindMissingInDB); fd == nil {
		t.Errorf("no %s finding in %v", KindMissingInDB, report.Findings())
	} else if fd.ActionID != f.actionID {
		t.Errorf("finding action = %s, want %s", fd.ActionID, f.actionID)
	}
	if stage.ActionType != 2 || stage.BlockNumber != 42 || stage.KeyID != hexKey([32]byte(f.action.KeyID)) {
		t.Errorf("chain fields not filled: %+v", stage)
	}
	if stage.DetailsHash != CheckSkipped || stage.Signature != CheckSkipped {
		t.Errorf("checks = %s/%s, want SKIPPED", stage.DetailsHash, stage.Signature)
	}
}

func TestAudit_Product_MissingOnChainAndPending(t *testing.T) {
	f := newFixture(t)
	product := f.productOf(t)
	s, _ := consistentFakes(f, product)
	l := &fakeLedger{}
	report, err := NewAuditor(s, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if report.Stages[0].Status != StatusMissingOnChain {
		t.Errorf("status = %s, want MISSING_ON_CHAIN", report.Stages[0].Status)
	}
	if findFinding(report.Findings(), KindMissingOnChain) == nil {
		t.Errorf("no %s finding in %v", KindMissingOnChain, report.Findings())
	}

	f.dbAction.ChainStatus = "PENDING"
	s2 := &fakeStore{
		byProduct: map[uuid.UUID][]Action{product: {f.dbAction}},
		details:   func(a Action) (Details, error) { return f.details(), nil },
	}
	report, err = NewAuditor(s2, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product pending: %v", err)
	}
	if report.Stages[0].Status != StatusPending {
		t.Errorf("status = %s, want PENDING", report.Stages[0].Status)
	}
	if len(report.Findings()) != 0 {
		t.Errorf("findings = %v, want none", report.Findings())
	}
}

func TestAudit_Product_Rejected(t *testing.T) {
	f := newFixture(t)
	product := f.productOf(t)
	s, l := consistentFakes(f, product)
	l.applied = map[uuid.UUID]ChainAction{}
	l.rejected = map[uuid.UUID]ChainRejection{f.actionID: {ActionID: f.actionID, Reason: 3, ItemIndex: 1, TxHash: common.HexToHash("0xdef2")}}
	report, err := NewAuditor(s, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	stage := report.Stages[0]
	if stage.Status != StatusRejected {
		t.Errorf("status = %s, want REJECTED", stage.Status)
	}
	if stage.RejectReason != 3 {
		t.Errorf("rejectReason = %d, want 3", stage.RejectReason)
	}
	if len(report.Findings()) != 0 {
		t.Errorf("findings = %v, want none", report.Findings())
	}
	if stage.DetailsHash != CheckSkipped || stage.Signer != CheckSkipped {
		t.Errorf("checks = %s/%s, want SKIPPED", stage.DetailsHash, stage.Signer)
	}
}

func TestAudit_Product_BadSignature(t *testing.T) {
	f := newFixture(t)
	product := f.productOf(t)
	s, l := consistentFakes(f, product)
	auth := f.onChainAuth()
	auth.WebAuthn.R[0] ^= 0xff
	l.auths[f.actionID] = auth
	report, err := NewAuditor(s, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	stage := report.Stages[0]
	if stage.Status != StatusApplied {
		t.Errorf("status = %s, want APPLIED", stage.Status)
	}
	if stage.Signature != CheckMismatch {
		t.Errorf("signature = %s, want MISMATCH", stage.Signature)
	}
	if findFinding(report.Findings(), KindSignature) == nil {
		t.Errorf("no %s finding in %v", KindSignature, report.Findings())
	}
	if stage.DetailsHash != CheckOK || stage.Items != CheckOK || stage.Signer != CheckOK {
		t.Errorf("unrelated checks = %s/%s/%s, want OK", stage.DetailsHash, stage.Items, stage.Signer)
	}
}

func TestAudit_Product_SignerMismatch(t *testing.T) {
	f := newFixture(t)
	product := f.productOf(t)
	s, l := consistentFakes(f, product)
	ca := f.chainAction()
	ca.KeyID = [32]byte{0xee}
	l.applied[f.actionID] = ca
	l.keys[ca.KeyID] = Key{QX: [32]byte(f.vectors.Signer.QX), QY: [32]byte(f.vectors.Signer.QY)}
	report, err := NewAuditor(s, l).Product(context.Background(), product)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	stage := report.Stages[0]
	if stage.Signer != CheckMismatch {
		t.Errorf("signer = %s, want MISMATCH", stage.Signer)
	}
	if findFinding(report.Findings(), KindSigner) == nil {
		t.Errorf("no %s finding in %v", KindSigner, report.Findings())
	}
	if stage.Signature != CheckOK || stage.DetailsHash != CheckOK {
		t.Errorf("unrelated checks = %s/%s, want OK", stage.Signature, stage.DetailsHash)
	}
}

func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %s: %v", s, err)
	}
	return id
}
