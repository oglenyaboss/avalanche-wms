package products

import "context"

type repository interface {
	Recent(ctx context.Context, limit int) ([]RecentProduct, error)
	Timeline(ctx context.Context, key string) (Timeline, error)
}

type Service struct {
	repo repository
}

func NewService(repo repository) *Service { return &Service{repo: repo} }

func (s *Service) Recent(ctx context.Context, limit int) ([]RecentProduct, error) {
	return s.repo.Recent(ctx, limit)
}

func (s *Service) Timeline(ctx context.Context, key string) (Timeline, error) {
	return s.repo.Timeline(ctx, key)
}
