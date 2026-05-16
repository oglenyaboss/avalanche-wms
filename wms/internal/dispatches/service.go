package dispatches

import (
	"context"

	"wms/internal/domain"

	"github.com/google/uuid"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetDispatches(ctx context.Context, filter DispatchFilter) ([]domain.OutboundDispatch, error) {
	x, err := s.repo.GetDispatchesByFilter(ctx, filter)
	if err != nil {
		return []domain.OutboundDispatch{}, err
	}

	return x, nil
}

func (s *Service) GetDispatchByID(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error) {
	dsp, err := s.repo.GetDispatchByID(ctx, dispID)
	if err != nil {
		return domain.OutboundDispatch{}, err
	}
	return dsp, nil
}

func (s *Service) CreateNewDispatch(ctx context.Context, query *NewDispatchQuery) (domain.OutboundDispatch, error) {
	result, err := s.repo.CreateNewDispatch(ctx, query)
	if err != nil {
		return domain.OutboundDispatch{}, err
	}
	return result, nil
}
