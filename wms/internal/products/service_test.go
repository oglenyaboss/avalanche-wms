package products

import (
	"context"
	"errors"
	"testing"
)

type fakeRepo struct {
	recent      []RecentProduct
	recentLimit int
	timeline    Timeline
	timelineErr error
}

func (f *fakeRepo) Recent(_ context.Context, limit int) ([]RecentProduct, error) {
	f.recentLimit = limit
	return f.recent, nil
}
func (f *fakeRepo) Timeline(_ context.Context, _ string) (Timeline, error) {
	return f.timeline, f.timelineErr
}

func TestService_Timeline_PropagatesNotFound(t *testing.T) {
	svc := NewService(&fakeRepo{timelineErr: ErrProductNotFound})
	_, err := svc.Timeline(context.Background(), "nope")
	if !errors.Is(err, ErrProductNotFound) {
		t.Fatalf("err = %v, want ErrProductNotFound", err)
	}
}

func TestService_Recent_PassesLimit(t *testing.T) {
	repo := &fakeRepo{}
	if _, err := NewService(repo).Recent(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if repo.recentLimit != 7 {
		t.Fatalf("limit = %d, want 7", repo.recentLimit)
	}
}
