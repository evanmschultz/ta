package server

import (
	"context"
	"net/http"
)

// DocsRenderer is the consumer-side interface for documentation rendering
// (roadmap, schema, and other reference docs). Handlers delegate to
// DocsRenderer methods instead of reaching into internal/ops directly.
type DocsRenderer interface {
	RenderRoadmap(ctx context.Context, w http.ResponseWriter, req *http.Request) error
	RenderSchema(ctx context.Context, w http.ResponseWriter, req *http.Request) error
}

// WithDocsRenderer sets the docs renderer dependency and returns the server
// for method chaining.
func (s *Server) WithDocsRenderer(dr DocsRenderer) *Server {
	if dr != nil {
		s.docsRenderer = dr
	}
	return s
}

// registerDocsRoute registers the GET /roadmap and GET /schema handlers.
func (s *Server) registerDocsRoute() error {
	s.mux.HandleFunc("GET /roadmap", func(w http.ResponseWriter, req *http.Request) {
		if s.docsRenderer == nil {
			http.Error(w, "internal server error: docs renderer not configured", http.StatusInternalServerError)
			return
		}
		err := s.docsRenderer.RenderRoadmap(req.Context(), w, req)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	})

	s.mux.HandleFunc("GET /schema", func(w http.ResponseWriter, req *http.Request) {
		if s.docsRenderer == nil {
			http.Error(w, "internal server error: docs renderer not configured", http.StatusInternalServerError)
			return
		}
		err := s.docsRenderer.RenderSchema(req.Context(), w, req)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	})

	return nil
}
