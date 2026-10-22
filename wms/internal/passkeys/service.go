package passkeys

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/ledger"
	"wms/internal/signedaction"
)

const registrationSessionTTL = 5 * time.Minute

type repository interface {
	GetMember(ctx context.Context, userID uuid.UUID) (Member, error)
	ListOrgMembers(ctx context.Context, orgID uuid.UUID) ([]Member, error)
	LatestPasskey(ctx context.Context, userID uuid.UUID) (*domain.Passkey, error)
	LatestPasskeysByOrg(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]domain.Passkey, error)
	InsertPasskey(ctx context.Context, p domain.Passkey) error
	UpdatePasskeyStatus(ctx context.Context, credentialID []byte, status domain.PasskeyStatus) error
	RejectPendingPasskey(ctx context.Context, credentialID []byte) error
	PasskeyByKeyID(ctx context.Context, keyID [32]byte) (*domain.Passkey, error)
	PasskeyByCredentialID(ctx context.Context, id []byte) (*domain.Passkey, error)
	InsertKeyAction(ctx context.Context, a KeyAction) error
	KeyAction(ctx context.Context, actionID uuid.UUID) (*KeyAction, error)
	LatestConfirmations(ctx context.Context, orgID uuid.UUID) (map[string]KeyConfirmation, error)
	SubmitKeyAction(ctx context.Context, s KeyActionSubmission) error
}

// KeyStatusGetter is the slice of *ledger.Client used to read keys' registry
// status (test seam; nil disables status sync).
type KeyStatusGetter interface {
	GetRegistryKeys(ctx context.Context, keyIDs []string) (map[string]ledger.RegistryKey, error)
}

type pending struct {
	data    webauthn.SessionData
	expires time.Time
}

type OrgMember struct {
	UserID       uuid.UUID
	Username     string
	Role         domain.UserRole
	Passkey      *domain.Passkey
	Confirmation *KeyConfirmation
}

type OrganizationOverview struct {
	Organization domain.Organization
	Members      []OrgMember
}

// AdminKeyExport is the admin public key in the format read by the owner's CLI.
type AdminKeyExport struct {
	OrgID string `json:"orgId"`
	Name  string `json:"name"`
	QX    string `json:"qx"`
	QY    string `json:"qy"`
}

type Service struct {
	repo  repository
	wa    *webauthn.WebAuthn
	chain KeyStatusGetter
	now   func() time.Time

	// ponytail: сессии регистрации в памяти процесса; при нескольких репликах WMS — хранить в БД или Redis.
	mu       sync.Mutex
	sessions map[uuid.UUID]pending
}

func NewService(repo repository, wa *webauthn.WebAuthn, chain KeyStatusGetter) *Service {
	return &Service{
		repo:     repo,
		wa:       wa,
		chain:    chain,
		now:      time.Now,
		sessions: make(map[uuid.UUID]pending),
	}
}

func (s *Service) BeginRegistration(ctx context.Context, userID uuid.UUID) (*protocol.CredentialCreation, error) {
	member, err := s.authorize(ctx, userID, domain.UserRoleAdmin, domain.UserRoleOperator)
	if err != nil {
		return nil, err
	}
	if err := s.ensureNoLiveKey(ctx, userID); err != nil {
		return nil, err
	}

	creation, session, err := s.wa.BeginRegistration(
		webauthnUser{id: member.UserID, username: member.Username},
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithCredentialParameters([]protocol.CredentialParameter{{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256}}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.BeginRegistration: %w", err)
	}

	s.mu.Lock()
	s.sessions[userID] = pending{data: *session, expires: s.now().Add(registrationSessionTTL)}
	s.mu.Unlock()
	return creation, nil
}

func (s *Service) FinishRegistration(ctx context.Context, userID uuid.UUID, body io.Reader) (domain.Passkey, error) {
	member, err := s.authorize(ctx, userID, domain.UserRoleAdmin, domain.UserRoleOperator)
	if err != nil {
		return domain.Passkey{}, err
	}
	if err := s.ensureNoLiveKey(ctx, userID); err != nil {
		return domain.Passkey{}, err
	}

	session, err := s.takeSession(userID)
	if err != nil {
		return domain.Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(body)
	if err != nil {
		return domain.Passkey{}, fmt.Errorf("%w: %v", ErrInvalidRegistration, err)
	}
	cred, err := s.wa.CreateCredential(webauthnUser{id: member.UserID, username: member.Username}, session, parsed)
	if err != nil {
		return domain.Passkey{}, fmt.Errorf("%w: %v", ErrInvalidRegistration, err)
	}
	if !cred.Flags.UserVerified {
		return domain.Passkey{}, fmt.Errorf("%w: user verification required", ErrInvalidRegistration)
	}
	key, err := p256Coordinates(cred.PublicKey)
	if err != nil {
		return domain.Passkey{}, fmt.Errorf("%w: %v", ErrInvalidRegistration, err)
	}

	p := domain.Passkey{
		CredentialID: cred.ID,
		UserID:       member.UserID,
		OrgID:        *member.OrgID,
		Key:          key,
		KeyID:        signedaction.KeyID(key),
		Role:         member.Role,
		Status:       domain.PasskeyStatusPending,
		CreatedAt:    s.now(),
	}
	if err := s.repo.InsertPasskey(ctx, p); err != nil {
		return domain.Passkey{}, fmt.Errorf("passkeys.Service.FinishRegistration: %w", err)
	}
	return p, nil
}

func (s *Service) MyPasskey(ctx context.Context, userID uuid.UUID) (*domain.Passkey, error) {
	p, err := s.repo.LatestPasskey(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("passkeys.Service.MyPasskey: %w", err)
	}
	if p != nil {
		s.syncStatuses(ctx, []*domain.Passkey{p})
	}
	return p, nil
}

func (s *Service) Organization(ctx context.Context, userID uuid.UUID) (OrganizationOverview, error) {
	member, err := s.authorize(ctx, userID, domain.UserRoleAdmin)
	if err != nil {
		return OrganizationOverview{}, err
	}
	orgID := *member.OrgID

	users, err := s.repo.ListOrgMembers(ctx, orgID)
	if err != nil {
		return OrganizationOverview{}, fmt.Errorf("passkeys.Service.Organization members: %w", err)
	}
	keys, err := s.repo.LatestPasskeysByOrg(ctx, orgID)
	if err != nil {
		return OrganizationOverview{}, fmt.Errorf("passkeys.Service.Organization keys: %w", err)
	}
	confirmations, err := s.repo.LatestConfirmations(ctx, orgID)
	if err != nil {
		return OrganizationOverview{}, fmt.Errorf("passkeys.Service.Organization confirmations: %w", err)
	}

	out := OrganizationOverview{
		Organization: domain.Organization{ID: orgID, Name: member.OrgName},
		Members:      make([]OrgMember, 0, len(users)),
	}
	var live []*domain.Passkey
	for _, u := range users {
		m := OrgMember{UserID: u.UserID, Username: u.Username, Role: u.Role}
		if p, ok := keys[u.UserID]; ok {
			m.Passkey = &p
			live = append(live, &p)
			if c, ok := confirmations[string(p.CredentialID)]; ok {
				m.Confirmation = &c
			}
		}
		out.Members = append(out.Members, m)
	}
	s.syncStatuses(ctx, live)
	return out, nil
}

func (s *Service) AdminKeyExport(ctx context.Context, userID uuid.UUID) (AdminKeyExport, error) {
	member, err := s.authorize(ctx, userID, domain.UserRoleAdmin)
	if err != nil {
		return AdminKeyExport{}, err
	}
	p, err := s.repo.LatestPasskey(ctx, userID)
	if err != nil {
		return AdminKeyExport{}, fmt.Errorf("passkeys.Service.AdminKeyExport: %w", err)
	}
	if p == nil {
		return AdminKeyExport{}, ErrPasskeyNotFound
	}
	s.syncStatuses(ctx, []*domain.Passkey{p})
	if p.Status != domain.PasskeyStatusPending && p.Status != domain.PasskeyStatusActive {
		return AdminKeyExport{}, ErrPasskeyNotFound
	}

	return AdminKeyExport{
		OrgID: member.OrgID.String(),
		Name:  member.OrgName,
		QX:    "0x" + hex.EncodeToString(p.Key.X[:]),
		QY:    "0x" + hex.EncodeToString(p.Key.Y[:]),
	}, nil
}

// authorize loads the caller from the database (never from the request) and
// checks that they belong to an organization and hold one of the given roles.
func (s *Service) authorize(ctx context.Context, userID uuid.UUID, roles ...domain.UserRole) (Member, error) {
	member, err := s.repo.GetMember(ctx, userID)
	if err != nil {
		return Member{}, fmt.Errorf("passkeys.Service.authorize: %w", err)
	}
	if member.OrgID == nil {
		return Member{}, ErrNoOrganization
	}
	if !slices.Contains(roles, member.Role) {
		return Member{}, ErrForbidden
	}
	return member, nil
}

func (s *Service) ensureNoLiveKey(ctx context.Context, userID uuid.UUID) error {
	p, err := s.repo.LatestPasskey(ctx, userID)
	if err != nil {
		return fmt.Errorf("passkeys.Service.ensureNoLiveKey: %w", err)
	}
	if p != nil && (p.Status == domain.PasskeyStatusPending || p.Status == domain.PasskeyStatusActive) {
		return ErrPasskeyExists
	}
	return nil
}

// takeSession removes and returns the user's registration session. Sessions are
// single-use: a second finish for the same begin fails.
func (s *Service) takeSession(userID uuid.UUID) (webauthn.SessionData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.sessions[userID]
	if !ok {
		return webauthn.SessionData{}, fmt.Errorf("%w: no registration session", ErrInvalidRegistration)
	}
	delete(s.sessions, userID)
	if s.now().After(p.expires) {
		return webauthn.SessionData{}, fmt.Errorf("%w: registration session expired", ErrInvalidRegistration)
	}
	return p.data, nil
}
