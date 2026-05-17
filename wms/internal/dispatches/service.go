package dispatches

import (
	"context"
	"fmt"

	"wms/internal/domain"

	"github.com/google/uuid"
)

type dispatchesRepository interface {
	WithTx(ctx context.Context, fn func(dispatchesRepository) error) error
	GetActualDispatchCode(ctx context.Context) (int, error)
	CreateDispatchCode(ctx context.Context) (string, error)
	CreateNewDispatch(ctx context.Context, disp *NewDispatchQuery, dispCode string) (domain.OutboundDispatch, error)
	GetDispatchByID(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error)
	GetDispatchesByFilter(ctx context.Context, filter DispatchFilter) ([]domain.OutboundDispatch, error)
}

type Service struct {
	repo dispatchesRepository
}

func NewService(repo dispatchesRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetDispatches(ctx context.Context, filter DispatchFilter) ([]domain.OutboundDispatch, error) {
	dispatches, err := s.repo.GetDispatchesByFilter(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("dispatches.Service.GetDispatches: %w", err)
	}
	return dispatches, nil
}

func (s *Service) GetDispatchByID(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error) {
	dsp, err := s.repo.GetDispatchByID(ctx, dispID)
	if err != nil {
		return domain.OutboundDispatch{}, fmt.Errorf("dispatches.Service.GetDispatchByID: %w", err)
	}
	return dsp, nil
}

func (s *Service) CreateNewDispatch(ctx context.Context, query *NewDispatchQuery) (domain.OutboundDispatch, error) {
	var result domain.OutboundDispatch

	err := s.repo.WithTx(ctx, func(txRepo dispatchesRepository) error {
		dispatchCode, err := txRepo.CreateDispatchCode(ctx)
		if err != nil {
			return fmt.Errorf("dispatches.Service.CreateNewDispatch get code: %w", err)
		}

		result, err = txRepo.CreateNewDispatch(ctx, query, dispatchCode)
		if err != nil {
			return fmt.Errorf("dispatches.Service.CreateNewDispatch insert: %w", err)
		}
		return nil
	})

	if err != nil {
		return domain.OutboundDispatch{}, err
	}
	return result, nil
}
