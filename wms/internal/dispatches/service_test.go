package dispatches

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"wms/internal/domain"

	"github.com/google/uuid"
)

type mockDispatchesRepo struct {
	withTxFn                func(ctx context.Context, fn func(dispatchesRepository) error) error
	getActualDispatchCodeFn func(ctx context.Context) (int, error)
	createDispatchCodeFn    func(ctx context.Context) (string, error)
	createNewDispatchFn     func(ctx context.Context, disp *NewDispatchQuery, dispCode string) (domain.OutboundDispatch, error)
	getDispatchByIDFn       func(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error)
	getDispatchesByFilterFn func(ctx context.Context, filter DispatchFilter) ([]domain.OutboundDispatch, error)
}

func (m *mockDispatchesRepo) WithTx(ctx context.Context, fn func(dispatchesRepository) error) error {
	if m.withTxFn != nil {
		return m.withTxFn(ctx, fn)
	}
	return fn(m)
}

func (m *mockDispatchesRepo) GetActualDispatchCode(ctx context.Context) (int, error) {
	if m.getActualDispatchCodeFn != nil {
		return m.getActualDispatchCodeFn(ctx)
	}
	panic("GetActualDispatchCode not configured")
}

func (m *mockDispatchesRepo) CreateDispatchCode(ctx context.Context) (string, error) {
	if m.createDispatchCodeFn != nil {
		return m.createDispatchCodeFn(ctx)
	}
	panic("CreateDispatchCode not configured")
}

func (m *mockDispatchesRepo) CreateNewDispatch(ctx context.Context, disp *NewDispatchQuery, dispCode string) (domain.OutboundDispatch, error) {
	if m.createNewDispatchFn != nil {
		return m.createNewDispatchFn(ctx, disp, dispCode)
	}
	panic("CreateNewDispatch not configured")
}

func (m *mockDispatchesRepo) GetDispatchByID(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error) {
	if m.getDispatchByIDFn != nil {
		return m.getDispatchByIDFn(ctx, dispID)
	}
	panic("GetDispatchByID not configured")
}

func (m *mockDispatchesRepo) GetDispatchesByFilter(ctx context.Context, filter DispatchFilter) ([]domain.OutboundDispatch, error) {
	if m.getDispatchesByFilterFn != nil {
		return m.getDispatchesByFilterFn(ctx, filter)
	}
	panic("GetDispatchesByFilter not configured")
}

func TestCreateNewDispatch_HappyPath(t *testing.T) {
	destID := uuid.New()
	query := &NewDispatchQuery{
		DestinationID: destID,
		VehicleNumber: "A123BC77",
		DriverName:    "Иванов",
		ScheduledAt:   time.Now().Add(24 * time.Hour),
	}

	var capturedCode string
	expected := domain.OutboundDispatch{
		DispatchID:  uuid.New(),
		WarehouseID: 1,
	}

	mock := &mockDispatchesRepo{
		createDispatchCodeFn: func(_ context.Context) (string, error) {
			return "DSP-2026-0517-001", nil
		},
		createNewDispatchFn: func(_ context.Context, _ *NewDispatchQuery, dispCode string) (domain.OutboundDispatch, error) {
			capturedCode = dispCode
			return expected, nil
		},
	}

	svc := NewService(mock)
	got, err := svc.CreateNewDispatch(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.DispatchID != expected.DispatchID {
		t.Fatalf("expected dispatch ID %s, got %s", expected.DispatchID, got.DispatchID)
	}
	if capturedCode != "DSP-2026-0517-001" {
		t.Fatalf("expected code DSP-2026-0517-001, got %s", capturedCode)
	}
}

func TestCreateNewDispatch_DestinationNotFound(t *testing.T) {
	mock := &mockDispatchesRepo{
		createDispatchCodeFn: func(_ context.Context) (string, error) {
			return "DSP-2026-0517-001", nil
		},
		createNewDispatchFn: func(_ context.Context, _ *NewDispatchQuery, _ string) (domain.OutboundDispatch, error) {
			return domain.OutboundDispatch{}, ErrDestinationNotFound
		},
	}

	svc := NewService(mock)
	_, err := svc.CreateNewDispatch(context.Background(), &NewDispatchQuery{
		DestinationID: uuid.New(),
		VehicleNumber: "X",
		DriverName:    "X",
		ScheduledAt:   time.Now().Add(time.Hour),
	})
	if !errors.Is(err, ErrDestinationNotFound) {
		t.Fatalf("expected ErrDestinationNotFound, got %v", err)
	}
}

func TestCreateNewDispatch_CodeGenerationError(t *testing.T) {
	codeErr := errors.New("db connection lost")
	mock := &mockDispatchesRepo{
		createDispatchCodeFn: func(_ context.Context) (string, error) {
			return "", codeErr
		},
	}

	svc := NewService(mock)
	_, err := svc.CreateNewDispatch(context.Background(), &NewDispatchQuery{
		DestinationID: uuid.New(),
		VehicleNumber: "X",
		DriverName:    "X",
		ScheduledAt:   time.Now().Add(time.Hour),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetDispatches_HappyPath(t *testing.T) {
	expected := []domain.OutboundDispatch{
		{DispatchID: uuid.New(), DispatchCode: "DSP-001"},
		{DispatchID: uuid.New(), DispatchCode: "DSP-002"},
	}
	mock := &mockDispatchesRepo{
		getDispatchesByFilterFn: func(_ context.Context, _ DispatchFilter) ([]domain.OutboundDispatch, error) {
			return expected, nil
		},
	}

	svc := NewService(mock)
	got, err := svc.GetDispatches(context.Background(), DispatchFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 dispatches, got %d", len(got))
	}
}

func TestGetDispatches_EmptyResult(t *testing.T) {
	mock := &mockDispatchesRepo{
		getDispatchesByFilterFn: func(_ context.Context, _ DispatchFilter) ([]domain.OutboundDispatch, error) {
			return []domain.OutboundDispatch{}, nil
		},
	}

	svc := NewService(mock)
	got, err := svc.GetDispatches(context.Background(), DispatchFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 dispatches, got %d", len(got))
	}
}

func TestGetDispatchByID_HappyPath(t *testing.T) {
	id := uuid.New()
	expected := domain.OutboundDispatch{DispatchID: id, DispatchCode: "DSP-001"}
	mock := &mockDispatchesRepo{
		getDispatchByIDFn: func(_ context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error) {
			if dispID != id {
				t.Fatalf("expected ID %s, got %s", id, dispID)
			}
			return expected, nil
		},
	}

	svc := NewService(mock)
	got, err := svc.GetDispatchByID(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.DispatchID != id {
		t.Fatalf("expected dispatch ID %s, got %s", id, got.DispatchID)
	}
}

func TestGetDispatchByID_NotFound(t *testing.T) {
	mock := &mockDispatchesRepo{
		getDispatchByIDFn: func(_ context.Context, _ uuid.UUID) (domain.OutboundDispatch, error) {
			return domain.OutboundDispatch{}, nil
		},
	}

	svc := NewService(mock)
	got, err := svc.GetDispatchByID(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != (domain.OutboundDispatch{}) {
		t.Fatalf("expected zero-value dispatch, got %+v", got)
	}
}

func TestCreateNewDispatch_CodeFormat(t *testing.T) {
	re := regexp.MustCompile(`^DSP-\d{4}-\d{4}-\d{3}$`)

	mock := &mockDispatchesRepo{
		createDispatchCodeFn: func(_ context.Context) (string, error) {
			return "DSP-" + time.Now().Format("2006-0102") + "-005", nil
		},
		createNewDispatchFn: func(_ context.Context, _ *NewDispatchQuery, dispCode string) (domain.OutboundDispatch, error) {
			if !re.MatchString(dispCode) {
				t.Fatalf("dispatch code %q doesn't match format DSP-YYYY-MMDD-NNN", dispCode)
			}
			return domain.OutboundDispatch{DispatchCode: dispCode}, nil
		},
	}

	svc := NewService(mock)
	_, err := svc.CreateNewDispatch(context.Background(), &NewDispatchQuery{
		DestinationID: uuid.New(),
		VehicleNumber: "X",
		DriverName:    "X",
		ScheduledAt:   time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
