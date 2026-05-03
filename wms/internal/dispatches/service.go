package dispatches

import (
	"context"
	"strconv"
	"time"

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

func (s *Service) GetDispatchById(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error) {
	dsp, err := s.repo.GetDispatchById(ctx, dispID)
	if err != nil {
		return domain.OutboundDispatch{}, err
	}
	return dsp, nil
}

func (s *Service) CreateDispatchCode(ctx context.Context) (string, error) {
	NNN, err := s.repo.GetActualDispatchCode(ctx)
	if err != nil {
		return "", err
	}
	strNNN := strconv.Itoa(NNN + 1)
	for len(strNNN) < 3 {
		strNNN = "0" + strNNN
	}
	dispatchCode := "DSP-" + time.Now().Format("2006-0102") + "-" + strNNN
	return dispatchCode, nil
}

func (s *Service) CreateNewDispatch(ctx context.Context, query *NewDispatchQuery) (domain.OutboundDispatch, error) {
	dispatchCode, err := s.CreateDispatchCode(ctx)
	if err != nil {
		return domain.OutboundDispatch{}, err
	}
	dispatch, err := s.repo.CreateNewDispatch(ctx, query, dispatchCode)
	if err != nil {
		return domain.OutboundDispatch{}, err
	}

	return dispatch, nil
}
