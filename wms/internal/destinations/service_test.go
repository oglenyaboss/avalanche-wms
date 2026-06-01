package destinations

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wms/internal/domain"
)

// fakeRepo реализует destinationsRepository, фиксируя переданный фильтр.
type fakeRepo struct {
	gotWarehouseID int64
	called         bool
	rows           []domain.Destination
	err            error
}

func (f *fakeRepo) GetDestinations(_ context.Context, warehouseID int64) ([]domain.Destination, error) {
	f.called = true
	f.gotWarehouseID = warehouseID
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func strptr(s string) *string { return &s }

func TestService_GetDestinations_PassesFilterAndReturnsRows(t *testing.T) {
	addr := strptr("г. Москва, ул. Маросейка, 5")
	want := []domain.Destination{
		{DestinationID: uuid.New(), Code: "SHOP-5", Name: "Магазин №5", Address: addr, WarehouseID: 1},
		{DestinationID: uuid.New(), Code: "SHOP-9", Name: "Магазин №9", Address: nil, WarehouseID: 1},
	}
	repo := &fakeRepo{rows: want}
	svc := NewService(repo)

	got, err := svc.GetDestinations(context.Background(), 1)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.gotWarehouseID != 1 {
		t.Fatalf("warehouse filter not passed through: got %d, want 1", repo.gotWarehouseID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestService_GetDestinations_NoFilter(t *testing.T) {
	repo := &fakeRepo{rows: []domain.Destination{}}
	svc := NewService(repo)

	got, err := svc.GetDestinations(context.Background(), 0)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.gotWarehouseID != 0 {
		t.Fatalf("warehouse_id 0 must be passed verbatim (no filter): got %d", repo.gotWarehouseID)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %d rows", len(got))
	}
}

func TestService_GetDestinations_WrapsRepoError(t *testing.T) {
	sentinel := errors.New("db down")
	repo := &fakeRepo{err: sentinel}
	svc := NewService(repo)

	got, err := svc.GetDestinations(context.Background(), 0)

	if got != nil {
		t.Fatalf("expected nil rows on error, got %+v", got)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel error, got %v", err)
	}
	if !strings.Contains(err.Error(), "destinations.Service.GetDestinations:") {
		t.Fatalf("expected error wrapped with service prefix, got %q", err.Error())
	}
}
