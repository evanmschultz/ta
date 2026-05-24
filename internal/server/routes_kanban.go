package server

import (
	"context"
	"net/http"
)

// KanbanRenderer is the consumer-side interface for the /kanban page. The
// kanban view groups cascade action items by state into 4 columns
// (todo / in_progress / complete / failed) as pure server-rendered HTML.
type KanbanRenderer interface {
	RenderKanban(ctx context.Context, w http.ResponseWriter, req *http.Request) error
}

// WithKanbanRenderer sets the kanban renderer dependency.
func (s *Server) WithKanbanRenderer(kr KanbanRenderer) *Server {
	if kr != nil {
		s.kanbanRenderer = kr
	}
	return s
}

func (s *Server) registerKanbanRoute() error {
	s.mux.HandleFunc("GET /kanban", func(w http.ResponseWriter, req *http.Request) {
		if s.kanbanRenderer == nil {
			http.Error(w, "internal server error: kanban renderer not configured", http.StatusInternalServerError)
			return
		}
		if err := s.kanbanRenderer.RenderKanban(req.Context(), w, req); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	})
	return nil
}
