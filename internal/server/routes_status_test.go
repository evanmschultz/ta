package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestServe_MethodNotAllowed verifies that all routes correctly reject
// non-GET HTTP methods with 405 Method Not Allowed.
//
// This test is cross-cutting: it probes every registered route with POST,
// PUT, DELETE, and other non-GET methods to ensure the HTTP semantics
// are correctly enforced at the mux level.
func TestServe_MethodNotAllowed(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		wantStatusCode int
	}{
		// Root route / should reject non-GET
		{
			name:           "POST / returns 405 Method Not Allowed",
			method:         http.MethodPost,
			path:           "/",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PUT / returns 405 Method Not Allowed",
			method:         http.MethodPut,
			path:           "/",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "DELETE / returns 405 Method Not Allowed",
			method:         http.MethodDelete,
			path:           "/",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PATCH / returns 405 Method Not Allowed",
			method:         http.MethodPatch,
			path:           "/",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		// HEAD is intentionally omitted: per HTTP semantics + Go 1.22+ mux
		// behavior, HEAD requests are routed to the matching GET handler
		// (the response body is then stripped). They are not 405.
		{
			name:           "OPTIONS / returns 405 Method Not Allowed",
			method:         http.MethodOptions,
			path:           "/",
			wantStatusCode: http.StatusMethodNotAllowed,
		},

		// /cascade/{id} route should reject non-GET
		{
			name:           "POST /cascade/{id} returns 405 Method Not Allowed",
			method:         http.MethodPost,
			path:           "/cascade/drop_001.drop.foo",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PUT /cascade/{id} returns 405 Method Not Allowed",
			method:         http.MethodPut,
			path:           "/cascade/drop_001.drop.foo",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "DELETE /cascade/{id} returns 405 Method Not Allowed",
			method:         http.MethodDelete,
			path:           "/cascade/drop_001.drop.foo",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PATCH /cascade/{id} returns 405 Method Not Allowed",
			method:         http.MethodPatch,
			path:           "/cascade/drop_001.drop.foo",
			wantStatusCode: http.StatusMethodNotAllowed,
		},

		// /roadmap route should reject non-GET
		{
			name:           "POST /roadmap returns 405 Method Not Allowed",
			method:         http.MethodPost,
			path:           "/roadmap",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PUT /roadmap returns 405 Method Not Allowed",
			method:         http.MethodPut,
			path:           "/roadmap",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "DELETE /roadmap returns 405 Method Not Allowed",
			method:         http.MethodDelete,
			path:           "/roadmap",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PATCH /roadmap returns 405 Method Not Allowed",
			method:         http.MethodPatch,
			path:           "/roadmap",
			wantStatusCode: http.StatusMethodNotAllowed,
		},

		// /schema route should reject non-GET
		{
			name:           "POST /schema returns 405 Method Not Allowed",
			method:         http.MethodPost,
			path:           "/schema",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PUT /schema returns 405 Method Not Allowed",
			method:         http.MethodPut,
			path:           "/schema",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "DELETE /schema returns 405 Method Not Allowed",
			method:         http.MethodDelete,
			path:           "/schema",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PATCH /schema returns 405 Method Not Allowed",
			method:         http.MethodPatch,
			path:           "/schema",
			wantStatusCode: http.StatusMethodNotAllowed,
		},

		// /search route should reject non-GET
		{
			name:           "POST /search returns 405 Method Not Allowed",
			method:         http.MethodPost,
			path:           "/search?q=test",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PUT /search returns 405 Method Not Allowed",
			method:         http.MethodPut,
			path:           "/search?q=test",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "DELETE /search returns 405 Method Not Allowed",
			method:         http.MethodDelete,
			path:           "/search?q=test",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:           "PATCH /search returns 405 Method Not Allowed",
			method:         http.MethodPatch,
			path:           "/search?q=test",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Bind: "127.0.0.1", Port: 4321})

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			srv.mux.ServeHTTP(rec, req)

			if got, want := rec.Code, tt.wantStatusCode; got != want {
				t.Fatalf("status code = %d, want %d", got, want)
			}
		})
	}
}

// TestServe_UnknownPathReturns404 verifies that requests to unregistered
// paths return 404 Not Found. This test documents the expected behavior
// for paths that do not match any registered route.
func TestServe_UnknownPathReturns404(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		wantStatusCode int
	}{
		{
			name:           "GET unknown path returns 404",
			method:         http.MethodGet,
			path:           "/unknown",
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "GET /cascade/id/extra returns 404",
			method:         http.MethodGet,
			path:           "/cascade/drop_001.drop.foo/extra",
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "GET /nonexistent/path returns 404",
			method:         http.MethodGet,
			path:           "/nonexistent/path",
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "POST unknown path returns 404",
			method:         http.MethodPost,
			path:           "/unknown",
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "PUT unknown path returns 404",
			method:         http.MethodPut,
			path:           "/unknown",
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "DELETE unknown path returns 404",
			method:         http.MethodDelete,
			path:           "/unknown",
			wantStatusCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Bind: "127.0.0.1", Port: 4321})

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			srv.mux.ServeHTTP(rec, req)

			if got, want := rec.Code, tt.wantStatusCode; got != want {
				t.Fatalf("status code = %d, want %d", got, want)
			}
		})
	}
}
