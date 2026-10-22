// Package signedops carries multi-part signed warehouse actions (ADR-0001):
// an operation is a draft of N parts, each part is signed by the operator's
// passkey and submitted into the outbox in one database transaction.
package signedops

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedaction"
)

// MaxItemsPerAction is the warehouse limit on items per signed action (ADR §10).
const MaxItemsPerAction = 2000

// Part is one signed action of an operation: its own action id, salt and challenge.
type Part struct {
	ActionID    uuid.UUID
	ItemIDs     []uuid.UUID // по возрастанию itemId (ADR §3)
	Salt        [32]byte
	DetailsHash [32]byte
	Challenge   [32]byte
}

// Operation is a draft of one warehouse operation split into signed parts.
type Operation struct {
	OperationID        uuid.UUID
	ActionType         uint8
	CreatedBy          uuid.UUID
	SignerCredentialID []byte
	SignerKeyID        [32]byte // не хранится в warehouse_actions; заполняется вызывающим из passkey
	Details            json.RawMessage
	Parts              []Part // в порядке part_index
	Submitted          bool
}

// NewOperation sorts items by itemId (ADR §3), splits them into consecutive
// parts of at most MaxItemsPerAction and derives a fresh action id, salt and
// challenge for every part. detailsHash receives the part's own sorted items.
// Zero items yield a valid operation with zero parts.
func NewOperation(actionType uint8, createdBy uuid.UUID, signer *domain.Passkey, details json.RawMessage, items []uuid.UUID, detailsHash func(salt [32]byte, items []uuid.UUID) [32]byte) (*Operation, error) {
	operationID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("signedops.NewOperation operation id: %w", err)
	}
	op := &Operation{
		OperationID:        operationID,
		ActionType:         actionType,
		CreatedBy:          createdBy,
		SignerCredentialID: signer.CredentialID,
		SignerKeyID:        signer.KeyID,
		Details:            details,
	}
	sorted := signedaction.SortByItemID(items)
	for start := 0; start < len(sorted); start += MaxItemsPerAction {
		end := start + MaxItemsPerAction
		if end > len(sorted) {
			end = len(sorted)
		}
		part, err := newPart(actionType, sorted[start:end], detailsHash)
		if err != nil {
			return nil, err
		}
		op.Parts = append(op.Parts, *part)
	}
	return op, nil
}

func newPart(actionType uint8, items []uuid.UUID, detailsHash func(salt [32]byte, items []uuid.UUID) [32]byte) (*Part, error) {
	actionID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("signedops.NewOperation action id: %w", err)
	}
	var salt [32]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return nil, fmt.Errorf("signedops.NewOperation salt: %w", err)
	}
	details := detailsHash(salt, items)
	return &Part{
		ActionID:    actionID,
		ItemIDs:     items,
		Salt:        salt,
		DetailsHash: details,
		Challenge:   signedaction.Challenge(actionID, actionType, signedaction.ItemsHash(items), details),
	}, nil
}

// envelopeDoc matches the exact JSON keys of the signed-action envelope (ADR §9)
// for a warehouse action: no subjectKey.
type envelopeDoc struct {
	Version     int                           `json:"version"`
	ActionID    string                        `json:"actionId"`
	ActionType  uint8                         `json:"actionType"`
	ItemIDs     []string                      `json:"itemIds"`
	DetailsHash string                        `json:"detailsHash"`
	KeyID       string                        `json:"keyId"`
	WebAuthn    signedaction.EnvelopeWebAuthn `json:"webauthn"`
}

// Envelopes verifies every assertion against its part's challenge and renders
// one outbox payload per part, in part order.
func Envelopes(op *Operation, signer *domain.Passkey, assertions map[uuid.UUID]signedaction.Assertion, rpID string, origins []string) ([][]byte, error) {
	if len(assertions) != len(op.Parts) {
		return nil, fmt.Errorf("%w: %d assertions for %d parts", ErrInvalidAssertion, len(assertions), len(op.Parts))
	}
	out := make([][]byte, 0, len(op.Parts))
	for _, p := range op.Parts {
		a, ok := assertions[p.ActionID]
		if !ok {
			return nil, fmt.Errorf("%w: no assertion for action %s", ErrInvalidAssertion, p.ActionID)
		}
		if !bytes.Equal(a.CredentialID, signer.CredentialID) {
			return nil, fmt.Errorf("%w: credential mismatch", ErrInvalidAssertion)
		}
		wa, err := signedaction.Verify(a, p.Challenge, signer.Key, rpID, origins)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidAssertion, err)
		}
		itemIDs := make([]string, len(p.ItemIDs))
		for i, id := range p.ItemIDs {
			itemIDs[i] = id.String()
		}
		raw, err := json.Marshal(envelopeDoc{
			Version:     1,
			ActionID:    p.ActionID.String(),
			ActionType:  op.ActionType,
			ItemIDs:     itemIDs,
			DetailsHash: signedaction.Hex32(p.DetailsHash),
			KeyID:       signedaction.Hex32(signer.KeyID),
			WebAuthn:    signedaction.NewEnvelopeWebAuthn(wa),
		})
		if err != nil {
			return nil, fmt.Errorf("signedops.Envelopes marshal: %w", err)
		}
		out = append(out, raw)
	}
	return out, nil
}
