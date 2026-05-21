package server

import (
	"context"
	"net/http"
)

// ViewRenderer is the consumer-side interface for cascade tree rendering.
// Handlers delegate to ViewRenderer methods instead of reaching into
// internal/ops directly. This keeps internal/server pure HTTP machinery.
type ViewRenderer interface {
	RenderCascadeTree(ctx context.Context, w http.ResponseWriter, req *http.Request) error
}

// WithViewRenderer sets the view renderer dependency and returns the server
// for method chaining. This allows route registration after construction
// without reshaping the bootstrap seam.
func (s *Server) WithViewRenderer(vr ViewRenderer) *Server {
	if vr != nil {
		s.viewRenderer = vr
	}
	return s
}

// registerRootRoute registers the GET / handler that delegates to the view layer.
// The "/{$}" suffix is the Go 1.22+ literal-end-of-path terminator: without
// it, "GET /" matches every path as a catch-all prefix, swallowing 404s for
// unknown URLs. With "/{$}", only the exact "/" path is matched.
func (s *Server) registerRootRoute() error {
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, req *http.Request) {
		if s.viewRenderer == nil {
			http.Error(w, "internal server error: view renderer not configured", http.StatusInternalServerError)
			return
		}
		err := s.viewRenderer.RenderCascadeTree(req.Context(), w, req)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	})
	return nil
}
