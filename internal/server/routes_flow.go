package server

import (
	"context"
	"net/http"
)

// FlowRenderer is the consumer-side interface for the /flow interactive
// flowchart page. Handlers delegate page rendering to the view layer; the
// flowchart's data comes from the /api/cascade/graph.json endpoint via a
// client-side fetch.
type FlowRenderer interface {
	RenderFlow(ctx context.Context, w http.ResponseWriter, req *http.Request) error
}

// WithFlowRenderer sets the flow renderer dependency.
func (s *Server) WithFlowRenderer(fr FlowRenderer) *Server {
	if fr != nil {
		s.flowRenderer = fr
	}
	return s
}

func (s *Server) registerFlowRoute() error {
	s.mux.HandleFunc("GET /flow", func(w http.ResponseWriter, req *http.Request) {
		if s.flowRenderer == nil {
			http.Error(w, "internal server error: flow renderer not configured", http.StatusInternalServerError)
			return
		}
		if err := s.flowRenderer.RenderFlow(req.Context(), w, req); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	})
	return nil
}
