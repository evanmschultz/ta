package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockSearchRenderer struct {
	renderSearchCalls   int
	renderSearchErr     error
	capturedQuery       string
	capturedStatusCode  int
	capturedStatusSetBy string
}

func (m *mockSearchRenderer) RenderSearch(ctx context.Context, w http.ResponseWriter, req *http.Request, query string) error {
	m.renderSearchCalls++
	m.capturedQuery = query
	if m.renderSearchErr != nil {
		return m.renderSearchErr
	}
	w.WriteHeader(http.StatusOK)
	m.capturedStatusCode = http.StatusOK
	m.capturedStatusSetBy = "mock"
	_, _ = io.WriteString(w, "search results for: "+query)
	return nil
}

// TestServe_SearchRouteParsesQueryString verifies that the /search route
// correctly extracts the query parameter from the request URL.
func TestServe_SearchRouteParsesQueryString(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		path             string
		searchRenderer   SearchRenderer
		wantStatusCode   int
		wantBody         string
		wantCapturedQ    string
		wantRendererCall bool
	}{
		{
			name:             "GET /search with valid q parameter",
			method:           http.MethodGet,
			path:             "/search?q=cascade",
			searchRenderer:   &mockSearchRenderer{},
			wantStatusCode:   http.StatusOK,
			wantBody:         "search results for: cascade",
			wantCapturedQ:    "cascade",
			wantRendererCall: true,
		},
		{
			name:             "GET /search with complex query string",
			method:           http.MethodGet,
			path:             "/search?q=drop_001.drop.foo",
			searchRenderer:   &mockSearchRenderer{},
			wantStatusCode:   http.StatusOK,
			wantBody:         "search results for: drop_001.drop.foo",
			wantCapturedQ:    "drop_001.drop.foo",
			wantRendererCall: true,
		},
		{
			name:             "GET /search with URL-encoded query",
			method:           http.MethodGet,
			path:             "/search?q=hello%20world",
			searchRenderer:   &mockSearchRenderer{},
			wantStatusCode:   http.StatusOK,
			wantBody:         "search results for: hello world",
			wantCapturedQ:    "hello world",
			wantRendererCall: true,
		},
		{
			name:             "GET /search without q parameter",
			method:           http.MethodGet,
			path:             "/search",
			searchRenderer:   &mockSearchRenderer{},
			wantStatusCode:   http.StatusOK,
			wantBody:         "search results for: ",
			wantCapturedQ:    "",
			wantRendererCall: true,
		},
		{
			name:             "GET /search with empty q parameter",
			method:           http.MethodGet,
			path:             "/search?q=",
			searchRenderer:   &mockSearchRenderer{},
			wantStatusCode:   http.StatusOK,
			wantBody:         "search results for: ",
			wantCapturedQ:    "",
			wantRendererCall: true,
		},
		{
			name:             "GET /search nil renderer returns 500",
			method:           http.MethodGet,
			path:             "/search?q=test",
			searchRenderer:   nil,
			wantStatusCode:   http.StatusInternalServerError,
			wantRendererCall: false,
		},
		{
			name:             "GET /search renderer error returns 500",
			method:           http.MethodGet,
			path:             "/search?q=error",
			searchRenderer:   &mockSearchRenderer{renderSearchErr: io.ErrUnexpectedEOF},
			wantStatusCode:   http.StatusInternalServerError,
			wantCapturedQ:    "error",
			wantRendererCall: true,
		},
		{
			name:             "GET /search with multiple q parameters uses first",
			method:           http.MethodGet,
			path:             "/search?q=first&q=second",
			searchRenderer:   &mockSearchRenderer{},
			wantStatusCode:   http.StatusOK,
			wantBody:         "search results for: first",
			wantCapturedQ:    "first",
			wantRendererCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Bind: "127.0.0.1", Port: 4321})
			if tt.searchRenderer != nil {
				srv.WithSearchRenderer(tt.searchRenderer)
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

			if tt.searchRenderer != nil {
				if mock, ok := tt.searchRenderer.(*mockSearchRenderer); ok {
					if tt.wantRendererCall {
						if got, want := mock.renderSearchCalls, 1; got != want {
							t.Fatalf("renderSearch calls = %d, want %d", got, want)
						}
						if got, want := mock.capturedQuery, tt.wantCapturedQ; got != want {
							t.Fatalf("captured query = %q, want %q", got, want)
						}
					} else {
						if got, want := mock.renderSearchCalls, 0; got != want {
							t.Fatalf("renderSearch calls = %d, want %d (should not be called)", got, want)
						}
					}
				}
			}
		})
	}
}
