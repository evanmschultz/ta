package server

import (
	"context"
	"net/http"
)

// GraphAPIRenderer is the consumer-side interface for /api/cascade/graph.json
// — the read-only JSON endpoint that powers the client-side flowchart on the
// /flow page. Returns the full cascade graph (nodes + edges) as JSON.
type GraphAPIRenderer interface {
	RenderGraphAPI(ctx context.Context, w http.ResponseWriter, req *http.Request) error
}

// WithGraphAPIRenderer sets the graph-API renderer dependency.
func (s *Server) WithGraphAPIRenderer(gar GraphAPIRenderer) *Server {
	if gar != nil {
		s.graphAPIRenderer = gar
	}
	return s
}

func (s *Server) registerGraphAPIRoute() error {
	s.mux.HandleFunc("GET /api/cascade/graph.json", func(w http.ResponseWriter, req *http.Request) {
		if s.graphAPIRenderer == nil {
			http.Error(w, "internal server error: graph API renderer not configured", http.StatusInternalServerError)
			return
		}
		if err := s.graphAPIRenderer.RenderGraphAPI(req.Context(), w, req); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	})
	return nil
}
