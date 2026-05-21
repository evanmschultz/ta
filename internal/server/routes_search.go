package server

import (
	"context"
	"net/http"
)

// SearchRenderer is the consumer-side interface for search result rendering.
// It handles parsing search queries and rendering the search results page.
type SearchRenderer interface {
	RenderSearch(ctx context.Context, w http.ResponseWriter, req *http.Request, query string) error
}

// WithSearchRenderer sets the search renderer dependency and returns the server
// for method chaining.
func (s *Server) WithSearchRenderer(sr SearchRenderer) *Server {
	if sr != nil {
		s.searchRenderer = sr
	}
	return s
}

// registerSearchRoute registers the GET /search?q=... handler.
func (s *Server) registerSearchRoute() error {
	s.mux.HandleFunc("GET /search", func(w http.ResponseWriter, req *http.Request) {
		if s.searchRenderer == nil {
			http.Error(w, "internal server error: search renderer not configured", http.StatusInternalServerError)
			return
		}

		query := req.URL.Query().Get("q")
		err := s.searchRenderer.RenderSearch(req.Context(), w, req, query)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	})

	return nil
}
