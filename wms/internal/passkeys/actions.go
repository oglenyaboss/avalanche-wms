package passkeys

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// KeyConfirmation is the confirmation state of the latest submitted action for a key.
type KeyConfirmation struct {
	ActionID     uuid.UUID
	ActionType   uint8
	State        signedaction.ConfirmationState
	RejectReason *int16
}

// KeyActionSubmission carries the outbox row written when a draft is submitted.
type KeyActionSubmission struct {
	ActionID           uuid.UUID
	ActionType         uint8
	TargetCredentialID []byte
	SignerCredentialID []byte
	PayloadHash        string // challenge, 64 lowercase hex chars, no 0x
	Payload            []byte // envelope JSON
}

// KeyActionDraft is a stored draft plus what the admin's browser needs to sign it.
type KeyActionDraft struct {
	Action      KeyAction
	Subject     domain.P256Point
	DetailsHash [32]byte
	SignerKeyID [32]byte
	RPID        string
}

// DraftKeyRegister stores a KeyRegister draft confirming the operator passkey
// with the given key id and returns the signing materials for the admin's browser.
func (s *Service) DraftKeyRegister(ctx context.Context, userID uuid.UUID, targetKeyID string) (*KeyActionDraft, error) {
	return s.draftKeyAction(ctx, userID, signedaction.TypeKeyRegister, func(orgID uuid.UUID) (*domain.Passkey, error) {
		return s.pendingOperatorTarget(ctx, orgID, targetKeyID)
	})
}

// DraftKeyRevoke stores a KeyRevoke draft revoking the active operator passkey
// with the given key id and returns the signing materials for the admin's browser.
func (s *Service) DraftKeyRevoke(ctx context.Context, userID uuid.UUID, targetKeyID string) (*KeyActionDraft, error) {
	return s.draftKeyAction(ctx, userID, signedaction.TypeKeyRevoke, func(orgID uuid.UUID) (*domain.Passkey, error) {
		return s.activeOperatorTarget(ctx, orgID, targetKeyID)
	})
}

// draftKeyAction authorizes the admin, requires their active signer key,
// resolves the action's target key via target and stores the drafted key action.
func (s *Service) draftKeyAction(ctx context.Context, userID uuid.UUID, actionType uint8, target func(orgID uuid.UUID) (*domain.Passkey, error)) (*KeyActionDraft, error) {
	member, err := s.authorize(ctx, userID, domain.UserRoleAdmin)
	if err != nil {
		return nil, err
	}

	signer, err := s.ActiveSigner(ctx, userID)
	if err != nil {
		return nil, err
	}

	subject, err := target(*member.OrgID)
	if err != nil {
		return nil, err
	}

	actionID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.draftKeyAction action id: %w", err)
	}
	detailsHash := signedaction.KeyDetailsHash(subject.Key)
	action := KeyAction{
		ActionID:           actionID,
		OrgID:              *member.OrgID,
		ActionType:         actionType,
		TargetCredentialID: subject.CredentialID,
		SignerCredentialID: signer.CredentialID,
		Challenge:          signedaction.Challenge(actionID, actionType, signedaction.NoItems, detailsHash),
		CreatedBy:          userID,
	}
	if err := s.repo.InsertKeyAction(ctx, action); err != nil {
		return nil, fmt.Errorf("passkeys.Service.draftKeyAction insert: %w", err)
	}
	return &KeyActionDraft{
		Action:      action,
		Subject:     subject.Key,
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

// operatorTarget loads the operator passkey of org orgID with the given key id
// and refreshes its status from the on-chain registry.
func (s *Service) operatorTarget(ctx context.Context, orgID uuid.UUID, targetKeyID string) (*domain.Passkey, error) {
	keyID, err := parseKeyID(targetKeyID)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	target, err := s.repo.PasskeyByKeyID(ctx, keyID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.operatorTarget target: %w", err)
	}
	if target == nil || target.OrgID != orgID || target.Role != domain.UserRoleOperator {
		return nil, ErrPasskeyNotFound
	}
	s.syncStatus(ctx, target)
	return target, nil
}

// pendingOperatorTarget returns the PENDING operator passkey of org orgID with
// the given key id that has no confirmation in flight.
func (s *Service) pendingOperatorTarget(ctx context.Context, orgID uuid.UUID, targetKeyID string) (*domain.Passkey, error) {
	target, err := s.operatorTarget(ctx, orgID, targetKeyID)
	if err != nil {
		return nil, err
	}
	if target.Status != domain.PasskeyStatusPending {
		return nil, ErrTargetNotPending
	}
	if err := s.requireNoConfirmation(ctx, orgID, target, signedaction.ConfirmationSent, signedaction.ConfirmationApplied); err != nil {
		return nil, err
	}
	return target, nil
}

// activeOperatorTarget returns the ACTIVE operator passkey of org orgID with
// the given key id that has no confirmation still awaiting the chain.
func (s *Service) activeOperatorTarget(ctx context.Context, orgID uuid.UUID, targetKeyID string) (*domain.Passkey, error) {
	target, err := s.operatorTarget(ctx, orgID, targetKeyID)
	if err != nil {
		return nil, err
	}
	if target.Status != domain.PasskeyStatusActive {
		return nil, ErrTargetNotActive
	}
	if err := s.requireNoConfirmation(ctx, orgID, target, signedaction.ConfirmationSent); err != nil {
		return nil, err
	}
	return target, nil
}

// requireNoConfirmation fails with ErrActionInProgress when the latest
// submitted key action for the target is in one of the blocking states.
func (s *Service) requireNoConfirmation(ctx context.Context, orgID uuid.UUID, target *domain.Passkey, blocking ...signedaction.ConfirmationState) error {
	confirmations, err := s.repo.LatestConfirmations(ctx, orgID)
	if err != nil {
		return fmt.Errorf("passkeys.Service.confirmations: %w", err)
	}
	if c, ok := confirmations[string(target.CredentialID)]; ok && slices.Contains(blocking, c.State) {
		return ErrActionInProgress
	}
	return nil
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
	wa, err := signedaction.Verify(a, action.Challenge, signer.Key, s.wa.Config.RPID, s.wa.Config.RPOrigins)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAssertion, err)
	}

	payload, err := json.Marshal(keyActionEnvelope(action, target, signer.KeyID, wa))
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.SubmitKeyAction envelope: %w", err)
	}
	if err := s.repo.SubmitKeyAction(ctx, KeyActionSubmission{
		ActionID:           actionID,
		ActionType:         action.ActionType,
		TargetCredentialID: action.TargetCredentialID,
		SignerCredentialID: action.SignerCredentialID,
		PayloadHash:        hex.EncodeToString(action.Challenge[:]),
		Payload:            payload,
	}); err != nil {
		return nil, fmt.Errorf("passkeys.Service.SubmitKeyAction submit: %w", err)
	}
	return &KeyConfirmation{ActionID: actionID, ActionType: action.ActionType, State: signedaction.ConfirmationSent}, nil
}

// ActiveSigner returns the caller's latest passkey when it is ACTIVE in the
// on-chain registry; it signs every draft this user creates.
func (s *Service) ActiveSigner(ctx context.Context, userID uuid.UUID) (*domain.Passkey, error) {
	signer, err := s.repo.LatestPasskey(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.ActiveSigner: %w", err)
	}
	if signer == nil {
		return nil, ErrPasskeyNotFound
	}
	s.syncStatus(ctx, signer)
	if signer.Status != domain.PasskeyStatusActive {
		return nil, ErrSignerKeyNotActive
	}
	return signer, nil
}

// RelyingParty returns the WebAuthn RP ID and allowed origins of this service.
func (s *Service) RelyingParty() (rpID string, origins []string) {
	return s.wa.Config.RPID, s.wa.Config.RPOrigins
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
