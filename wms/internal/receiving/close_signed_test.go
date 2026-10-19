package receiving

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedaction"
	"wms/internal/signedaction/signedactiontest"
	"wms/internal/signedops"
)

func TestDraftCloseCargoplace_Success(t *testing.T) {
	cargoplaceID := uuid.New()
	operatorID := uuid.New()
	item := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID:     &domain.Cargoplace{CargoplaceID: cargoplaceID, Status: cargoplaceStatusTableInProgress},
		closableProductIDs: []uuid.UUID{item},
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}
	signerSource := &mockSignerSource{rpID: "localhost", origins: []string{signedactiontest.Origin}}
	signer, _ := signedSigner()
	signerSource.signer = signer

	op, rpID, err := closeService(repo, ops, signerSource).DraftCloseCargoplace(context.Background(), operatorID, cargoplaceID)
	if err != nil {
		t.Fatalf("DraftCloseCargoplace: %v", err)
	}
	if rpID != "localhost" {
		t.Fatalf("rpID = %q, want localhost", rpID)
	}
	if ops.inserted == nil || ops.inserted.OperationID != op.OperationID {
		t.Fatalf("operation not stored")
	}
	if op.ActionType != signedaction.TypeReceive {
		t.Errorf("actionType = %d, want 1", op.ActionType)
	}
	if len(op.Parts) != 1 || len(op.Parts[0].ItemIDs) != 1 || op.Parts[0].ItemIDs[0] != item {
		t.Fatalf("parts = %+v, want one part with the item", op.Parts)
	}

	var details struct {
		CargoplaceID uuid.UUID `json:"cargoplace_id"`
		OccurredAtMs int64     `json:"occurred_at_ms"`
	}
	if err := json.Unmarshal(op.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v (%s)", err, op.Details)
	}
	if details.CargoplaceID != cargoplaceID {
		t.Errorf("details cargoplace_id = %s, want %s", details.CargoplaceID, cargoplaceID)
	}
	if details.OccurredAtMs <= 0 || details.OccurredAtMs > time.Now().UnixMilli() {
		t.Errorf("occurred_at_ms = %d, want a fresh truncated ms timestamp", details.OccurredAtMs)
	}
	if time.UnixMilli(details.OccurredAtMs) != time.UnixMilli(details.OccurredAtMs).Truncate(time.Millisecond) {
		t.Errorf("occurred_at_ms = %d is not millisecond-precision", details.OccurredAtMs)
	}
	want := signedaction.ReceiveDetailsHash(op.Parts[0].Salt, cargoplaceID, operatorID, uint64(details.OccurredAtMs))
	if op.Parts[0].DetailsHash != want {
		t.Errorf("detailsHash = %x, want %x", op.Parts[0].DetailsHash, want)
	}
}

func TestDraftCloseCargoplace_Errors(t *testing.T) {
	cargoplaceID := uuid.New()
	operatorID := uuid.New()
	base := func() (*mockReceivingRepo, *mockOperationStore, *mockSignerSource) {
		repo := &mockReceivingRepo{
			cargoplaceByID:     &domain.Cargoplace{CargoplaceID: cargoplaceID, Status: cargoplaceStatusTableInProgress},
			closableProductIDs: []uuid.UUID{uuid.New()},
		}
		signer, _ := signedSigner()
		return repo, &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}, &mockSignerSource{signer: signer}
	}

	t.Run("invalid input", func(t *testing.T) {
		repo, ops, signer := base()
		if _, _, err := closeService(repo, ops, signer).DraftCloseCargoplace(context.Background(), operatorID, uuid.Nil); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("cargoplace not found", func(t *testing.T) {
		repo, ops, signer := base()
		repo.cargoplaceByID = nil
		if _, _, err := closeService(repo, ops, signer).DraftCloseCargoplace(context.Background(), operatorID, cargoplaceID); !errors.Is(err, ErrCargoplaceNotFound) {
			t.Fatalf("err = %v, want ErrCargoplaceNotFound", err)
		}
	})
	t.Run("not in progress", func(t *testing.T) {
		repo, ops, signer := base()
		repo.cargoplaceByID.Status = cargoplaceStatusTableClosed
		if _, _, err := closeService(repo, ops, signer).DraftCloseCargoplace(context.Background(), operatorID, cargoplaceID); !errors.Is(err, ErrCargoplaceNotInProgress) {
			t.Fatalf("err = %v, want ErrCargoplaceNotInProgress", err)
		}
	})
	t.Run("empty cargoplace", func(t *testing.T) {
		repo, ops, signer := base()
		repo.closableProductIDs = nil
		if _, _, err := closeService(repo, ops, signer).DraftCloseCargoplace(context.Background(), operatorID, cargoplaceID); !errors.Is(err, ErrCargoplaceEmpty) {
			t.Fatalf("err = %v, want ErrCargoplaceEmpty", err)
		}
	})
	t.Run("signer error propagates", func(t *testing.T) {
		repo, ops, signer := base()
		signerErr := errors.New("signer down")
		signer.signerErr = signerErr
		_, _, err := closeService(repo, ops, signer).DraftCloseCargoplace(context.Background(), operatorID, cargoplaceID)
		if !errors.Is(err, signerErr) {
			t.Fatalf("err = %v, want signer error propagated", err)
		}
	})
}

func TestSubmitCloseCargoplace_Errors(t *testing.T) {
	operatorID := uuid.New()
	items := []uuid.UUID{uuid.New(), uuid.New()}

	build := func(t *testing.T) (*mockReceivingRepo, *mockOperationStore, *mockSignerSource, *signedops.Operation, *ecdsa.PrivateKey) {
		t.Helper()
		cargoplaceID := uuid.New()
		op, signer, key := newSignedOperation(t, cargoplaceID, operatorID, items)
		repo := &mockReceivingRepo{
			cargoplaceByID: &domain.Cargoplace{CargoplaceID: cargoplaceID, Status: cargoplaceStatusTableInProgress},
			closeCargoplaceTxResult: &CloseCargoplaceTxResult{
				ExpectedSKUs:        []ExpectedSKU{},
				ReceivedSKUCounts:   []ReceivedSKUCount{},
				OutboxEventsCreated: len(op.Parts),
			},
		}
		ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{op.OperationID: op}}
		return repo, ops, &mockSignerSource{signer: signer}, op, key
	}

	t.Run("operation not found", func(t *testing.T) {
		repo, ops, signer, _, _ := build(t)
		_, err := closeService(repo, ops, signer).SubmitCloseCargoplace(context.Background(), operatorID, uuid.New(), nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("draft of another operator", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		_, err := closeService(repo, ops, signer).SubmitCloseCargoplace(context.Background(), uuid.New(), op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("wrong action type", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		op.ActionType = signedaction.TypePutAway
		_, err := closeService(repo, ops, signer).SubmitCloseCargoplace(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationNotFound) {
			t.Fatalf("err = %v, want ErrOperationNotFound", err)
		}
	})
	t.Run("already submitted", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		op.Submitted = true
		_, err := closeService(repo, ops, signer).SubmitCloseCargoplace(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrOperationSubmitted) {
			t.Fatalf("err = %v, want ErrOperationSubmitted", err)
		}
	})
	t.Run("signer credential mismatch", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		other, _ := signedSigner()
		other.CredentialID = []byte("other-credential")
		signer.signer = other
		_, err := closeService(repo, ops, signer).SubmitCloseCargoplace(context.Background(), operatorID, op.OperationID, nil)
		if !errors.Is(err, signedops.ErrSignerNotActive) {
			t.Fatalf("err = %v, want ErrSignerNotActive", err)
		}
	})
	t.Run("bad assertion, repo not called", func(t *testing.T) {
		repo, ops, signer, op, _ := build(t)
		assertion := signedactiontest.Sign(signedactiontest.NewKey(), op.Parts[0].Challenge)
		assertion.CredentialID = op.SignerCredentialID
		assertions := map[uuid.UUID]signedaction.Assertion{op.Parts[0].ActionID: assertion}

		_, err := closeService(repo, ops, signer).SubmitCloseCargoplace(context.Background(), operatorID, op.OperationID, assertions)
		if !errors.Is(err, signedops.ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
		if repo.closeCargoplaceParams != nil {
			t.Fatalf("repo called despite invalid assertion")
		}
	})
}

func TestSubmitCloseCargoplace_Success(t *testing.T) {
	operatorID := uuid.New()
	cargoplaceID := uuid.New()
	items := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	op, signerPasskey, key := newSignedOperation(t, cargoplaceID, operatorID, items)
	var details struct {
		CargoplaceID uuid.UUID `json:"cargoplace_id"`
		OccurredAtMs int64     `json:"occurred_at_ms"`
	}
	if err := json.Unmarshal(op.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}

	repo := &mockReceivingRepo{
		closeCargoplaceTxResult: &CloseCargoplaceTxResult{
			ExpectedSKUs:      []ExpectedSKU{},
			ReceivedSKUCounts: []ReceivedSKUCount{},
		},
	}
	ops := &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{op.OperationID: op}}
	signerSource := &mockSignerSource{signer: signerPasskey, rpID: signedactiontest.RPID, origins: []string{signedactiontest.Origin}}

	assertions := map[uuid.UUID]signedaction.Assertion{}
	for _, p := range op.Parts {
		a := signedactiontest.Sign(key, p.Challenge)
		a.CredentialID = signerPasskey.CredentialID
		assertions[p.ActionID] = a
	}

	result, err := closeService(repo, ops, signerSource).SubmitCloseCargoplace(context.Background(), operatorID, op.OperationID, assertions)
	if err != nil {
		t.Fatalf("SubmitCloseCargoplace: %v", err)
	}

	params := repo.closeCargoplaceParams
	if params == nil {
		t.Fatalf("repo not called")
	}
	if params.OperationID == nil || *params.OperationID != op.OperationID {
		t.Errorf("operationID = %v, want %s", params.OperationID, op.OperationID)
	}
	if params.CargoplaceID != cargoplaceID {
		t.Errorf("cargoplaceID = %s, want %s (details carries the draft's cargoplace)", params.CargoplaceID, cargoplaceID)
	}
	if !params.OccurredAt.Equal(time.UnixMilli(details.OccurredAtMs).UTC()) {
		t.Errorf("occurredAt = %v, want %v", params.OccurredAt, time.UnixMilli(details.OccurredAtMs).UTC())
	}
	if len(params.Payloads) != len(op.Parts) {
		t.Errorf("payloads = %d, want %d", len(params.Payloads), len(op.Parts))
	}
	if result.OperationID != op.OperationID || result.Status != cargoplaceStatusTableClosed || result.OutboxEventsCreated != len(op.Parts) {
		t.Errorf("result = %+v", result)
	}
}
