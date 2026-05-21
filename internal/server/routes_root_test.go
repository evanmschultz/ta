package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockViewRenderer is a test double that records calls to RenderCascadeTree.
type mockViewRenderer struct {
	renderCascadeTreeCalls int
	renderCascadeTreeErr   error
}

func (m *mockViewRenderer) RenderCascadeTree(ctx context.Context, w http.ResponseWriter, req *http.Request) error {
	m.renderCascadeTreeCalls++
	if m.renderCascadeTreeErr != nil {
		return m.renderCascadeTreeErr
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "cascade tree response")
	return nil
}

// TestServe_AsHTTP_CascadeTreeOnGet verifies that GET / is registered
// and correctly delegates to the injected view renderer through the HTTP surface.
func TestServe_AsHTTP_CascadeTreeOnGet(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		viewRenderer   ViewRenderer
		wantStatusCode int
		wantBody       string
	}{
		{
			name:           "GET / delegates to view renderer",
			method:         http.MethodGet,
			path:           "/",
			viewRenderer:   &mockViewRenderer{},
			wantStatusCode: http.StatusOK,
			wantBody:       "cascade tree response",
		},
		{
			name:           "GET / with nil view renderer returns 500",
			method:         http.MethodGet,
			path:           "/",
			viewRenderer:   nil,
			wantStatusCode: http.StatusInternalServerError,
		},
		{
			name:           "GET / view renderer error returns 500",
			method:         http.MethodGet,
			path:           "/",
			viewRenderer:   &mockViewRenderer{renderCascadeTreeErr: io.ErrUnexpectedEOF},
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Bind: "127.0.0.1", Port: 4321})
			if tt.viewRenderer != nil {
				srv.WithViewRenderer(tt.viewRenderer)
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

			// Verify the mock was called exactly once when renderer was set.
			if tt.viewRenderer != nil {
				if mock, ok := tt.viewRenderer.(*mockViewRenderer); ok {
					if got, want := mock.renderCascadeTreeCalls, 1; got != want {
						t.Fatalf("renderCascadeTree calls = %d, want %d", got, want)
					}
				}
			}
		})
	}
}
