package audit

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/sha3"

	"wms/internal/signedaction"
)

// stage reconciles one action: infrastructure errors are returned, domain
// mismatches become findings; the switch covers every db/applied/rejected combination.
func (a *Auditor) stage(ctx context.Context, db *Action, applied *ChainAction, rejected *ChainRejection) (Stage, error) {
	switch {
	case db == nil:
		s := Stage{
			Status:      StatusMissingInDB,
			DetailsHash: CheckSkipped,
			Items:       CheckSkipped,
			Signer:      CheckSkipped,
			Signature:   CheckSkipped,
			Findings:    []Finding{},
		}
		switch {
		case applied != nil:
			s.ActionID = applied.ActionID
			s.ActionType = applied.ActionType
			s.KeyID = hexKey(applied.KeyID)
			s.OrgID = applied.OrgID
			s.BlockNumber = applied.BlockNumber
			s.BlockTime = &applied.BlockTime
			s.TxHash = applied.TxHash.Hex()
		case rejected != nil:
			s.ActionID = rejected.ActionID
			s.RejectReason = rejected.Reason
			s.TxHash = rejected.TxHash.Hex()
		}
		s.Findings = append(s.Findings, Finding{ActionID: s.ActionID, Kind: KindMissingInDB, Detail: "action is on chain but not in DB"})
		return s, nil
	case applied != nil:
		return a.appliedStage(ctx, db, applied)
	case rejected != nil:
		return Stage{
			ActionID:     db.ActionID,
			ActionType:   db.ActionType,
			Status:       StatusRejected,
			OperatorID:   db.OperatorID,
			Operator:     db.Operator,
			OrgID:        db.OrgID,
			OrgName:      db.OrgName,
			KeyID:        hexKey(db.KeyID),
			TxHash:       rejected.TxHash.Hex(),
			RejectReason: rejected.Reason,
			DetailsHash:  CheckSkipped,
			Items:        CheckSkipped,
			Signer:       CheckSkipped,
			Signature:    CheckSkipped,
			Findings:     []Finding{},
		}, nil
	default:
		s := Stage{
			ActionID:    db.ActionID,
			ActionType:  db.ActionType,
			OperatorID:  db.OperatorID,
			Operator:    db.Operator,
			OrgID:       db.OrgID,
			OrgName:     db.OrgName,
			KeyID:       hexKey(db.KeyID),
			DetailsHash: CheckSkipped,
			Items:       CheckSkipped,
			Signer:      CheckSkipped,
			Signature:   CheckSkipped,
			Findings:    []Finding{},
		}
		if db.ChainStatus == "PENDING" || db.ChainStatus == "SENT" {
			if a.now().Sub(db.SubmittedAt) <= PendingStaleAfter {
				s.Status = StatusPending
				return s, nil
			}
			s.Status = StatusMissingOnChain
			s.Findings = append(s.Findings, Finding{ActionID: db.ActionID, Kind: KindMissingOnChain, Detail: "action is PENDING/SENT in DB for over 10m but not on chain"})
			return s, nil
		}
		s.Status = StatusMissingOnChain
		s.Findings = append(s.Findings, Finding{ActionID: db.ActionID, Kind: KindMissingOnChain, Detail: "action is in DB but not on chain"})
		return s, nil
	}
}

// appliedStage reconciles an applied action against the DB and the signature.
func (a *Auditor) appliedStage(ctx context.Context, db *Action, applied *ChainAction) (Stage, error) {
	s := Stage{
		ActionID:    applied.ActionID,
		ActionType:  applied.ActionType,
		Status:      StatusApplied,
		OperatorID:  db.OperatorID,
		Operator:    db.Operator,
		OrgID:       db.OrgID,
		OrgName:     db.OrgName,
		KeyID:       hexKey(applied.KeyID),
		BlockNumber: applied.BlockNumber,
		BlockTime:   &applied.BlockTime,
		TxHash:      applied.TxHash.Hex(),
		Findings:    []Finding{},
	}
	if db.ActionType != applied.ActionType {
		s.DetailsHash, s.Items, s.Signer, s.Signature = CheckSkipped, CheckSkipped, CheckSkipped, CheckSkipped
		s.Findings = append(s.Findings, Finding{ActionID: db.ActionID, Kind: KindType, Detail: fmt.Sprintf("db %d chain %d", db.ActionType, applied.ActionType)})
		return s, nil
	}
	d, err := a.checkDetailsHash(ctx, db, applied, &s)
	if err != nil {
		return Stage{}, fmt.Errorf("audit.Auditor.stage: %w", err)
	}
	if err := a.checkItems(ctx, db, &s); err != nil {
		return Stage{}, fmt.Errorf("audit.Auditor.stage: %w", err)
	}
	checkSigner(db, applied, d, &s)
	if err := a.checkSignature(ctx, db, applied, &s); err != nil {
		return Stage{}, fmt.Errorf("audit.Auditor.stage: %w", err)
	}
	return s, nil
}

func (a *Auditor) checkDetailsHash(ctx context.Context, db *Action, applied *ChainAction, s *Stage) (*Details, error) {
	d, err := a.store.Details(ctx, *db)
	if err != nil {
		if errors.Is(err, ErrDomainRowMissing) || errors.Is(err, ErrDomainInconsistent) {
			s.DetailsHash = CheckMismatch
			s.Findings = append(s.Findings, Finding{ActionID: db.ActionID, Kind: KindDetailsHash, Detail: err.Error()})
			return nil, nil
		}
		return nil, err
	}
	h, err := d.Hash()
	if err != nil {
		return nil, err
	}
	if h != applied.DetailsHash {
		s.DetailsHash = CheckMismatch
		s.Findings = append(s.Findings, Finding{ActionID: db.ActionID, Kind: KindDetailsHash, Detail: fmt.Sprintf("db %s chain %s", hexKey(h), hexKey(applied.DetailsHash))})
		return &d, nil
	}
	s.DetailsHash = CheckOK
	return &d, nil
}

func (a *Auditor) checkItems(ctx context.Context, db *Action, s *Stage) error {
	chainItems, err := a.ledger.ItemsOfAction(ctx, db.ActionID)
	if err != nil {
		return err
	}
	domainItems, err := a.store.DomainItems(ctx, *db)
	if err != nil {
		return err
	}
	chainSet := make(map[[32]byte]bool, len(chainItems))
	for _, it := range chainItems {
		chainSet[it] = true
	}
	dbSet := make(map[[32]byte]bool, len(domainItems))
	for _, p := range domainItems {
		dbSet[signedaction.ItemID(p)] = true
	}
	same := len(chainSet) == len(dbSet)
	if same {
		for it := range dbSet {
			if !chainSet[it] {
				same = false
				break
			}
		}
	}
	// a duplicated domain row leaves the set equal but must still fail the check
	duplicated := len(dbSet) != len(domainItems)
	if !same || duplicated {
		s.Items = CheckMismatch
		s.Findings = append(s.Findings, Finding{ActionID: db.ActionID, Kind: KindItems, Detail: fmt.Sprintf("db %d chain %d items", len(domainItems), len(chainSet))})
		return nil
	}
	s.Items = CheckOK
	return nil
}

// checkSigner requires the chain signer to be the DB key and org, the executor
// (created_by) to own that key and the signed details to name the executor.
func checkSigner(db *Action, applied *ChainAction, d *Details, s *Stage) {
	detailsOperator := "-"
	if d != nil {
		detailsOperator = d.OperatorID.String()
	}
	if applied.KeyID == db.KeyID && applied.OrgID == db.OrgID && db.KeyOwnerID == db.OperatorID && (d == nil || d.OperatorID == db.OperatorID) {
		s.Signer = CheckOK
		return
	}
	s.Signer = CheckMismatch
	s.Findings = append(s.Findings, Finding{ActionID: db.ActionID, Kind: KindSigner,
		Detail: fmt.Sprintf("db key %s org %s chain key %s org %s executor %s key owner %s details operator %s",
			hexKey(db.KeyID), db.OrgID, hexKey(applied.KeyID), applied.OrgID, db.OperatorID, db.KeyOwnerID, detailsOperator)})
}

func (a *Auditor) checkSignature(ctx context.Context, db *Action, applied *ChainAction, s *Stage) error {
	k, err := a.ledger.Key(ctx, applied.KeyID)
	if err != nil {
		return err
	}
	auth, err := a.ledger.Auth(ctx, applied.TxHash, db.ActionID)
	if err != nil {
		return err
	}
	itemsHash := itemsHashWords(auth.ItemIDs)
	challenge := signedaction.Challenge(db.ActionID, applied.ActionType, itemsHash, applied.DetailsHash)
	if err := signedaction.VerifyOnChain(auth.WebAuthn, challenge, k.Point); err != nil {
		s.Signature = CheckMismatch
		s.Findings = append(s.Findings, Finding{ActionID: db.ActionID, Kind: KindSignature, Detail: err.Error()})
		return nil
	}
	s.Signature = CheckOK
	return nil
}

// itemsHashWords is keccak256 of the concatenated 32-byte big-endian words of the calldata item ids.
func itemsHashWords(itemIDs []*big.Int) [32]byte {
	buf := make([]byte, 0, 32*len(itemIDs))
	for _, id := range itemIDs {
		var word [32]byte
		id.FillBytes(word[:])
		buf = append(buf, word[:]...)
	}
	return keccak256(buf)
}

func keccak256(parts ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

func hexKey(k [32]byte) string {
	return "0x" + hex.EncodeToString(k[:])
}
