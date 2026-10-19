package passkeys

import (
	"context"
	"encoding/hex"
	"log"

	"wms/internal/domain"
)

// syncStatus refreshes a PENDING/ACTIVE key from the on-chain registry. Chain
// errors are logged and the stored status is kept, so reads never fail on it.
// ponytail: статус тянется из цепи при чтении, по запросу на ключ; при большой организации — фоновая синхронизация.
func (s *Service) syncStatus(ctx context.Context, p *domain.Passkey) {
	if s.chain == nil || (p.Status != domain.PasskeyStatusPending && p.Status != domain.PasskeyStatusActive) {
		return
	}
	key, err := s.chain.GetRegistryKey(ctx, "0x"+hex.EncodeToString(p.KeyID[:]))
	if err != nil {
		log.Printf("passkeys: registry status for key %x: %v", p.KeyID, err)
		return
	}
	if !key.Found {
		return
	}
	next := domain.PasskeyStatusRevoked
	if key.Active {
		next = domain.PasskeyStatusActive
	}
	if next == p.Status {
		return
	}
	if err := s.repo.UpdatePasskeyStatus(ctx, p.CredentialID, next); err != nil {
		log.Printf("passkeys: update status for key %x: %v", p.KeyID, err)
		return
	}
	p.Status = next
}
