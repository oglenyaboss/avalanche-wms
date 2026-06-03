package products

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
)

type fakeSvc struct {
	recent      []RecentProduct
	gotLimit    int
	timeline    Timeline
	timelineErr error
}

func (f *fakeSvc) Recent(_ context.Context, limit int) ([]RecentProduct, error) {
	f.gotLimit = limit
	return f.recent, nil
}
func (f *fakeSvc) Timeline(_ context.Context, _ string) (Timeline, error) {
	return f.timeline, f.timelineErr
}

// asOperator injects an authenticated identity — RequireAdminOrOperator (top of
// each handler) is the real gate; the subrouter middleware only populates this
// context. Mirrors wms/internal/destinations/handler_test.go.
func asOperator(req *http.Request) *http.Request {
	ctx := auth.ContextWithIdentity(req.Context(), uuid.New(), domain.UserRoleOperator)
	return req.WithContext(ctx)
}

func serveProducts(svc service, path string) *httptest.ResponseRecorder {
	r := mux.NewRouter()
	sub := r.PathPrefix("/products").Subrouter()
	NewHandler(svc).RegisterRoutes(sub)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, asOperator(httptest.NewRequest(http.MethodGet, path, nil)))
	return rec
}

func TestGetTimeline_MissingKey400(t *testing.T) {
	rec := serveProducts(&fakeSvc{}, "/products/timeline")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGetTimeline_NotFound404(t *testing.T) {
	rec := serveProducts(&fakeSvc{timelineErr: ErrProductNotFound}, "/products/timeline?key=missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetTimeline_EmptyStepsIs200(t *testing.T) {
	tl := Timeline{Product: ProductHeader{ProductID: "p1"}, Steps: []TimelineStep{}}
	rec := serveProducts(&fakeSvc{timeline: tl}, "/products/timeline?key=p1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var env struct {
		Success bool     `json:"success"`
		Data    Timeline `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if !env.Success || env.Data.Steps == nil {
		t.Fatalf("want success with non-nil steps array, got %s", rec.Body.String())
	}
}

func TestGetRecent_ClampsLimit(t *testing.T) {
	svc := &fakeSvc{}
	serveProducts(svc, "/products/recent?limit=999")
	if svc.gotLimit != maxRecentLimit {
		t.Fatalf("limit = %d, want %d", svc.gotLimit, maxRecentLimit)
	}
}
