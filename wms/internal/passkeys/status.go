package passkeys

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"

	"wms/internal/domain"
)

// syncStatuses refreshes the statuses of all given keys with one registry request; chain errors are logged and the stored statuses are kept.
func (s *Service) syncStatuses(ctx context.Context, keys []*domain.Passkey) {
	if s.chain == nil {
		return
	}
	byID := make(map[string]*domain.Passkey, len(keys))
	for _, p := range keys {
		if p.Status == domain.PasskeyStatusPending || p.Status == domain.PasskeyStatusActive {
			byID["0x"+hex.EncodeToString(p.KeyID[:])] = p
		}
	}
	if len(byID) == 0 {
		return
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	statuses, err := s.chain.GetRegistryKeys(ctx, ids)
	if err != nil {
		log.Printf("passkeys: registry status for %d keys: %v", len(ids), err)
		return
	}
	for id, p := range byID {
		key, ok := statuses[id]
		if !ok || !key.Found {
			continue
		}
		next := domain.PasskeyStatusRevoked
		if key.Active {
			next = domain.PasskeyStatusActive
		}
		if next == p.Status {
			continue
		}
		if err := s.repo.UpdatePasskeyStatus(ctx, p.CredentialID, next); err != nil {
			log.Printf("passkeys: update status for key %x: %v", p.KeyID, err)
			continue
		}
		p.Status = next
	}
}

// confirmActive requires the on-chain registry to confirm right now that the
// key is active: a registry error is fatal for signing, so it is returned
// instead of falling back to the stored status.
func (s *Service) confirmActive(ctx context.Context, p *domain.Passkey) error {
	id := "0x" + hex.EncodeToString(p.KeyID[:])
	statuses, err := s.chain.GetRegistryKeys(ctx, []string{id})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	key, ok := statuses[id]
	if !ok || !key.Found {
		return ErrSignerKeyNotActive
	}
	next := domain.PasskeyStatusRevoked
	if key.Active {
		next = domain.PasskeyStatusActive
	}
	if next != p.Status {
		if err := s.repo.UpdatePasskeyStatus(ctx, p.CredentialID, next); err != nil {
			log.Printf("passkeys: update status for key %x: %v", p.KeyID, err)
		}
		p.Status = next
	}
	if next != domain.PasskeyStatusActive {
		return ErrSignerKeyNotActive
	}
	return nil
}
