package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockDocsRenderer struct {
	renderRoadmapCalls int
	renderRoadmapErr   error
	renderSchemaCalls  int
	renderSchemaErr    error
}

func (m *mockDocsRenderer) RenderRoadmap(ctx context.Context, w http.ResponseWriter, req *http.Request) error {
	m.renderRoadmapCalls++
	if m.renderRoadmapErr != nil {
		return m.renderRoadmapErr
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "roadmap response")
	return nil
}

func (m *mockDocsRenderer) RenderSchema(ctx context.Context, w http.ResponseWriter, req *http.Request) error {
	m.renderSchemaCalls++
	if m.renderSchemaErr != nil {
		return m.renderSchemaErr
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "schema response")
	return nil
}

// TestServe_RoadmapAndSchemaOnGet verifies that GET /roadmap and GET /schema
// are registered and correctly delegate to the docs renderer.
func TestServe_RoadmapAndSchemaOnGet(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		docsRenderer   DocsRenderer
		wantStatusCode int
		wantBody       string
	}{
		{
			name:           "GET /roadmap delegates to renderer",
			method:         http.MethodGet,
			path:           "/roadmap",
			docsRenderer:   &mockDocsRenderer{},
			wantStatusCode: http.StatusOK,
			wantBody:       "roadmap response",
		},
		{
			name:           "GET /schema delegates to renderer",
			method:         http.MethodGet,
			path:           "/schema",
			docsRenderer:   &mockDocsRenderer{},
			wantStatusCode: http.StatusOK,
			wantBody:       "schema response",
		},
		{
			name:           "GET /roadmap with nil renderer returns 500",
			method:         http.MethodGet,
			path:           "/roadmap",
			docsRenderer:   nil,
			wantStatusCode: http.StatusInternalServerError,
		},
		{
			name:           "GET /schema with nil renderer returns 500",
			method:         http.MethodGet,
			path:           "/schema",
			docsRenderer:   nil,
			wantStatusCode: http.StatusInternalServerError,
		},
		{
			name:           "GET /roadmap renderer error returns 500",
			method:         http.MethodGet,
			path:           "/roadmap",
			docsRenderer:   &mockDocsRenderer{renderRoadmapErr: io.ErrUnexpectedEOF},
			wantStatusCode: http.StatusInternalServerError,
		},
		{
			name:           "GET /schema renderer error returns 500",
			method:         http.MethodGet,
			path:           "/schema",
			docsRenderer:   &mockDocsRenderer{renderSchemaErr: io.ErrUnexpectedEOF},
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Bind: "127.0.0.1", Port: 4321})
			if tt.docsRenderer != nil {
				srv.WithDocsRenderer(tt.docsRenderer)
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

			// Verify delegation for roadmap and schema calls
			if tt.docsRenderer != nil {
				if mock, ok := tt.docsRenderer.(*mockDocsRenderer); ok {
					if tt.path == "/roadmap" {
						if got, want := mock.renderRoadmapCalls, 1; got != want {
							t.Fatalf("renderRoadmap calls = %d, want %d", got, want)
						}
					} else if tt.path == "/schema" {
						if got, want := mock.renderSchemaCalls, 1; got != want {
							t.Fatalf("renderSchema calls = %d, want %d", got, want)
						}
					}
				}
			}
		})
	}
}
