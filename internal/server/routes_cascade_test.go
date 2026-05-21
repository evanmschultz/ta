package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockCascadeDetailRenderer struct {
	renderCascadeDetailCalls int
	renderCascadeDetailErr   error
}

func (m *mockCascadeDetailRenderer) RenderCascadeDetail(ctx context.Context, w http.ResponseWriter, req *http.Request) error {
	m.renderCascadeDetailCalls++
	if m.renderCascadeDetailErr != nil {
		return m.renderCascadeDetailErr
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "cascade detail response")
	return nil
}

// TestServe_CascadeDetailRejectsUnknownRecord verifies that a request to
// /cascade/<id> for a cascade that does not exist returns 404.
func TestServe_CascadeDetailRejectsUnknownRecord(t *testing.T) {
	srv := New(Config{Bind: "127.0.0.1", Port: 4321})
	srv.WithCascadeDetailRenderer(&mockCascadeDetailRenderer{
		renderCascadeDetailErr: &NotFoundError{Message: "cascade not found"},
	})

	req := httptest.NewRequest(http.MethodGet, "/cascade/drop_001.drop.unknown", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
}

// TestServe_AsHTTP_CascadeDetailOnGet verifies that GET /cascade/{id} is registered
// and correctly delegates to the cascade detail renderer.
func TestServe_AsHTTP_CascadeDetailOnGet(t *testing.T) {
	tests := []struct {
		name                  string
		method                string
		path                  string
		cascadeDetailRenderer CascadeDetailRenderer
		wantStatusCode        int
		wantBody              string
	}{
		{
			name:                  "GET /cascade/{id} delegates to renderer",
			method:                http.MethodGet,
			path:                  "/cascade/drop_001.drop.foo",
			cascadeDetailRenderer: &mockCascadeDetailRenderer{},
			wantStatusCode:        http.StatusOK,
			wantBody:              "cascade detail response",
		},
		{
			name:                  "GET /cascade/{id} nil renderer returns 500",
			method:                http.MethodGet,
			path:                  "/cascade/drop_002.drop.bar",
			cascadeDetailRenderer: nil,
			wantStatusCode:        http.StatusInternalServerError,
		},
		{
			name:                  "GET /cascade/{id} NotFoundError returns 404",
			method:                http.MethodGet,
			path:                  "/cascade/drop_003.drop.notfound",
			cascadeDetailRenderer: &mockCascadeDetailRenderer{renderCascadeDetailErr: &NotFoundError{Message: "not found"}},
			wantStatusCode:        http.StatusNotFound,
		},
		{
			name:                  "GET /cascade/{id} other error returns 500",
			method:                http.MethodGet,
			path:                  "/cascade/drop_004.drop.error",
			cascadeDetailRenderer: &mockCascadeDetailRenderer{renderCascadeDetailErr: io.ErrUnexpectedEOF},
			wantStatusCode:        http.StatusInternalServerError,
		},
		{
			name:                  "GET /cascade/{id} complex id parameter",
			method:                http.MethodGet,
			path:                  "/cascade/drop_999.drop.planner_l2_b_cascade_detail",
			cascadeDetailRenderer: &mockCascadeDetailRenderer{},
			wantStatusCode:        http.StatusOK,
			wantBody:              "cascade detail response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Bind: "127.0.0.1", Port: 4321})
			if tt.cascadeDetailRenderer != nil {
				srv.WithCascadeDetailRenderer(tt.cascadeDetailRenderer)
			}

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			srv.mux.ServeHTTP(rec, req)

			if got, want := rec.Code, tt.wantStatusCode; got != want {
				t.Fatalf("status code = %d, want %d", got, want)
			}

			if tt.wantBody != "" {
				if got, want := rec.Body.String(), tt.wantBody; got != want {
					t.Fatalf("body = %q, want %q", got, want)
				}
			}

			if tt.cascadeDetailRenderer != nil {
				if mock, ok := tt.cascadeDetailRenderer.(*mockCascadeDetailRenderer); ok {
					if got, want := mock.renderCascadeDetailCalls, 1; got != want {
						t.Fatalf("renderCascadeDetail calls = %d, want %d", got, want)
					}
				}
			}
		})
	}
}
