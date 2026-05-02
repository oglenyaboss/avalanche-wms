package dispatches

import (
	"strconv"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetDispatches(filter DispatchFilter) ([]OutboundDispatch, error) {
	x, err := s.repo.GetDispatchesByFilter(filter)
	if err != nil {
		return []OutboundDispatch{}, err
	}

	return x, nil
}

func (s *Service) GetDispatchById(disp_id uuid.UUID) (OutboundDispatch, error) {
	dsp, err := s.repo.GetDispatchById(disp_id)
	if err != nil {
		return OutboundDispatch{}, nil
	}
	return dsp, nil
}

func (s *Service) CreateDispatchCode() (string, error) {
	NNN, err := s.repo.GetActualDispatchCode()
	if err != nil {
		return "", err
	}
	strNNN := strconv.Itoa(NNN)
	for len(strNNN) < 3 {
		strNNN = "0" + strNNN
	}
	dispatchCode := "DSP-" + time.Now().Format("2026-0102") + "-" + strNNN
	return dispatchCode, nil
}

func (s *Service) CreateNewDispatch(query *NewDispatchQuery) (OutboundDispatch, error) {
	dispatchCode, err := s.CreateDispatchCode()
	if err != nil {
		return OutboundDispatch{}, err
	}
	dispatch, err := s.repo.CreateNewDispatch(query, dispatchCode)
	if err != nil {
		return OutboundDispatch{}, err
	}

	return dispatch, nil
}
