package destinations

import (
	"context"
	"fmt"

	"wms/internal/domain"
)

// destinationsRepository — узкий интерфейс репозитория для мокабельности в тестах.
type destinationsRepository interface {
	GetDestinations(ctx context.Context, warehouseID int64) ([]domain.Destination, error)
}

type Service struct {
	repo destinationsRepository
}

func NewService(repo destinationsRepository) *Service {
	return &Service{repo: repo}
}

// GetDestinations возвращает магазины-получатели. warehouseID <= 0 означает «без
// фильтра» (вернуть все), любое положительное значение фильтрует по складу.
func (s *Service) GetDestinations(ctx context.Context, warehouseID int64) ([]domain.Destination, error) {
	dests, err := s.repo.GetDestinations(ctx, warehouseID)
	if err != nil {
		return nil, fmt.Errorf("destinations.Service.GetDestinations: %w", err)
	}
	return dests, nil
}
