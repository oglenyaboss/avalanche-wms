package passkeys

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedaction"
)

// KeyAction is a stored signed action: a draft until submitted_at is set.
type KeyAction struct {
	ActionID           uuid.UUID
	OrgID              uuid.UUID
	ActionType         uint8
	TargetCredentialID []byte
	SignerCredentialID []byte
	Challenge          [32]byte
	CreatedBy          uuid.UUID
	SubmittedAt        *time.Time
}

// ConfirmationState is the derived chain confirmation of a submitted key action.
type ConfirmationState string

const (
	ConfirmationSent     ConfirmationState = "SENT"
	ConfirmationApplied  ConfirmationState = "APPLIED"
	ConfirmationRejected ConfirmationState = "REJECTED"
	ConfirmationFailed   ConfirmationState = "FAILED"
)

// KeyConfirmation is the confirmation state of the latest submitted action for a key.
type KeyConfirmation struct {
	ActionID     uuid.UUID
	State        ConfirmationState
	RejectReason *int16
}

// KeyActionSubmission carries the outbox row written when a draft is submitted.
type KeyActionSubmission struct {
	ActionID           uuid.UUID
	TargetCredentialID []byte
	SignerCredentialID []byte
	PayloadHash        string // challenge, 64 lowercase hex chars, no 0x
	Payload            []byte // envelope JSON
}

// KeyActionDraft is a stored draft plus what the admin's browser needs to sign it.
type KeyActionDraft struct {
	Action      KeyAction
	SubjectQX   [32]byte
	SubjectQY   [32]byte
	DetailsHash [32]byte
	SignerKeyID [32]byte
	RPID        string
}

// DraftKeyRegister stores a KeyRegister draft confirming the operator passkey
// with the given key id and returns the signing materials for the admin's browser.
func (s *Service) DraftKeyRegister(ctx context.Context, userID uuid.UUID, targetKeyID string) (*KeyActionDraft, error) {
	member, err := s.authorize(ctx, userID, domain.UserRoleAdmin)
	if err != nil {
		return nil, err
	}
	orgID := *member.OrgID

	signer, err := s.repo.LatestPasskey(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.DraftKeyRegister signer: %w", err)
	}
	if signer == nil {
		return nil, ErrPasskeyNotFound
	}
	s.syncStatus(ctx, signer)
	if signer.Status != domain.PasskeyStatusActive {
		return nil, ErrSignerKeyNotActive
	}

	target, err := s.pendingOperatorTarget(ctx, orgID, targetKeyID)
	if err != nil {
		return nil, err
	}

	actionID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.DraftKeyRegister action id: %w", err)
	}
	detailsHash := signedaction.KeyDetailsHash(target.QX, target.QY)
	action := KeyAction{
		ActionID:           actionID,
		OrgID:              orgID,
		ActionType:         signedaction.TypeKeyRegister,
		TargetCredentialID: target.CredentialID,
		SignerCredentialID: signer.CredentialID,
		Challenge:          signedaction.Challenge(actionID, signedaction.TypeKeyRegister, signedaction.NoItems, detailsHash),
		CreatedBy:          userID,
	}
	if err := s.repo.InsertKeyAction(ctx, action); err != nil {
		return nil, fmt.Errorf("passkeys.Service.DraftKeyRegister insert: %w", err)
	}
	return &KeyActionDraft{
		Action:      action,
		SubjectQX:   target.QX,
		SubjectQY:   target.QY,
		DetailsHash: detailsHash,
		SignerKeyID: signer.KeyID,
		RPID:        s.wa.Config.RPID,
	}, nil
}

// RejectKey immediately rejects a PENDING operator passkey belonging to the
// caller's organization. Unlike key registration this is an administrative
// action, so it requires no signed action, draft or on-chain confirmation.
func (s *Service) RejectKey(ctx context.Context, userID uuid.UUID, targetKeyID string) error {
	member, err := s.authorize(ctx, userID, domain.UserRoleAdmin)
	if err != nil {
		return err
	}
	orgID := *member.OrgID

	target, err := s.pendingOperatorTarget(ctx, orgID, targetKeyID)
	if err != nil {
		return err
	}

	if err := s.repo.RejectPendingPasskey(ctx, target.CredentialID); err != nil {
		if errors.Is(err, ErrTargetNotPending) || errors.Is(err, ErrActionInProgress) {
			return err
		}
		return fmt.Errorf("passkeys.Service.RejectKey reject: %w", err)
	}
	return nil
}

// pendingOperatorTarget returns the PENDING operator passkey of org orgID with the given key id
// that has no confirmation in flight.
func (s *Service) pendingOperatorTarget(ctx context.Context, orgID uuid.UUID, targetKeyID string) (*domain.Passkey, error) {
	keyID, err := parseKeyID(targetKeyID)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	target, err := s.repo.PasskeyByKeyID(ctx, keyID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.pendingOperatorTarget target: %w", err)
	}
	if target == nil || target.OrgID != orgID || target.Role != domain.UserRoleOperator {
		return nil, ErrPasskeyNotFound
	}
	s.syncStatus(ctx, target)
	if target.Status != domain.PasskeyStatusPending {
		return nil, ErrTargetNotPending
	}

	confirmations, err := s.repo.LatestConfirmations(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.pendingOperatorTarget confirmations: %w", err)
	}
	if c, ok := confirmations[string(target.CredentialID)]; ok && (c.State == ConfirmationSent || c.State == ConfirmationApplied) {
		return nil, ErrActionInProgress
	}
	return target, nil
}

// SubmitKeyAction verifies the browser assertion over the draft's challenge and
// submits the draft: outbox row and submitted_at in one repository transaction.
func (s *Service) SubmitKeyAction(ctx context.Context, userID, actionID uuid.UUID, a signedaction.Assertion) (*KeyConfirmation, error) {
	if _, err := s.authorize(ctx, userID, domain.UserRoleAdmin); err != nil {
		return nil, err
	}

	action, err := s.repo.KeyAction(ctx, actionID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.SubmitKeyAction load: %w", err)
	}
	if action == nil || action.CreatedBy != userID {
		return nil, ErrActionNotFound
	}
	if action.SubmittedAt != nil {
		return nil, ErrActionAlreadySubmitted
	}

	signer, err := s.repo.PasskeyByCredentialID(ctx, action.SignerCredentialID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.SubmitKeyAction signer: %w", err)
	}
	if signer == nil {
		return nil, ErrPasskeyNotFound
	}
	target, err := s.repo.PasskeyByCredentialID(ctx, action.TargetCredentialID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.SubmitKeyAction target: %w", err)
	}
	if target == nil {
		return nil, ErrPasskeyNotFound
	}

	if !bytes.Equal(a.CredentialID, signer.CredentialID) {
		return nil, ErrInvalidAssertion
	}
	wa, err := signedaction.Verify(a, action.Challenge, signer.QX, signer.QY, s.wa.Config.RPID, s.wa.Config.RPOrigins)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAssertion, err)
	}

	payload, err := json.Marshal(keyActionEnvelope(action, target, signer.KeyID, wa))
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.SubmitKeyAction envelope: %w", err)
	}
	if err := s.repo.SubmitKeyAction(ctx, KeyActionSubmission{
		ActionID:           actionID,
		TargetCredentialID: action.TargetCredentialID,
		SignerCredentialID: action.SignerCredentialID,
		PayloadHash:        hex.EncodeToString(action.Challenge[:]),
		Payload:            payload,
	}); err != nil {
		return nil, fmt.Errorf("passkeys.Service.SubmitKeyAction submit: %w", err)
	}
	return &KeyConfirmation{ActionID: actionID, State: ConfirmationSent}, nil
}

// keyActionEnvelope matches the exact JSON keys of the signed-action envelope
// (contract §2) for a key action: no itemIds, no detailsHash.
type keyActionEnvelopeDoc struct {
	Version    int              `json:"version"`
	ActionID   string           `json:"actionId"`
	ActionType uint8            `json:"actionType"`
	ItemIDs    []string         `json:"itemIds"`
	SubjectKey subjectKeyJSON   `json:"subjectKey"`
	KeyID      string           `json:"keyId"`
	WebAuthn   webauthnAuthJSON `json:"webauthn"`
}

type subjectKeyJSON struct {
	QX string `json:"qx"`
	QY string `json:"qy"`
}

type webauthnAuthJSON struct {
	AuthenticatorData string `json:"authenticatorData"`
	ClientDataJSON    string `json:"clientDataJSON"`
	ChallengeIndex    int    `json:"challengeIndex"`
	TypeIndex         int    `json:"typeIndex"`
	R                 string `json:"r"`
	S                 string `json:"s"`
}

func keyActionEnvelope(action *KeyAction, target *domain.Passkey, signerKeyID [32]byte, wa signedaction.WebAuthn) keyActionEnvelopeDoc {
	return keyActionEnvelopeDoc{
		Version:    1,
		ActionID:   action.ActionID.String(),
		ActionType: action.ActionType,
		ItemIDs:    []string{},
		SubjectKey: subjectKeyJSON{QX: hexCoordinate(target.QX), QY: hexCoordinate(target.QY)},
		KeyID:      hexCoordinate(signerKeyID),
		WebAuthn: webauthnAuthJSON{
			AuthenticatorData: "0x" + hex.EncodeToString(wa.AuthenticatorData),
			ClientDataJSON:    wa.ClientDataJSON,
			ChallengeIndex:    wa.ChallengeIndex,
			TypeIndex:         wa.TypeIndex,
			R:                 "0x" + hex.EncodeToString(wa.R[:]),
			S:                 "0x" + hex.EncodeToString(wa.S[:]),
		},
	}
}

func hexCoordinate(v [32]byte) string {
	return "0x" + hex.EncodeToString(v[:])
}

// parseKeyID accepts "0x" followed by 64 hex characters.
func parseKeyID(s string) ([32]byte, error) {
	var out [32]byte
	raw, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(raw) != 32 || !strings.HasPrefix(s, "0x") {
		return out, fmt.Errorf("key id must be 0x + 64 hex chars")
	}
	copy(out[:], raw)
	return out, nil
}
