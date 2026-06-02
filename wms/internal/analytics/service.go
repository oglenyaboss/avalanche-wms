package analytics

import (
	"context"
	"fmt"
)

// analyticsRepository is the narrow read-only interface the service depends on,
// kept here (where it is used) for mockability.
type analyticsRepository interface {
	GetSummary(ctx context.Context) (SummaryReport, error)
	GetOnchain(ctx context.Context, failedLimit, committedLimit int) (OnchainReport, error)
	GetThroughput(ctx context.Context, days int) (ThroughputReport, error)
}

type Service struct {
	repo analyticsRepository
}

func NewService(repo analyticsRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetSummary(ctx context.Context) (SummaryReport, error) {
	rep, err := s.repo.GetSummary(ctx)
	if err != nil {
		return SummaryReport{}, fmt.Errorf("analytics.Service.GetSummary: %w", err)
	}
	return rep, nil
}

func (s *Service) GetOnchain(ctx context.Context, failedLimit, committedLimit int) (OnchainReport, error) {
	rep, err := s.repo.GetOnchain(ctx, failedLimit, committedLimit)
	if err != nil {
		return OnchainReport{}, fmt.Errorf("analytics.Service.GetOnchain: %w", err)
	}
	return rep, nil
}

func (s *Service) GetThroughput(ctx context.Context, days int) (ThroughputReport, error) {
	rep, err := s.repo.GetThroughput(ctx, days)
	if err != nil {
		return ThroughputReport{}, fmt.Errorf("analytics.Service.GetThroughput: %w", err)
	}
	return rep, nil
}
