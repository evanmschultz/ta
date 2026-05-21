package server

import (
	"context"
	"errors"
	"net/http"
)

// NotFoundError signals that a requested cascade record was not found.
type NotFoundError struct {
	Message string
}

func (e *NotFoundError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "not found"
}

// CascadeDetailRenderer is the consumer-side interface for cascade detail rendering.
type CascadeDetailRenderer interface {
	RenderCascadeDetail(ctx context.Context, w http.ResponseWriter, req *http.Request) error
}

// WithCascadeDetailRenderer sets the cascade detail renderer dependency.
func (s *Server) WithCascadeDetailRenderer(cdr CascadeDetailRenderer) *Server {
	if cdr != nil {
		s.cascadeDetailRenderer = cdr
	}
	return s
}

// registerCascadeRoute registers the GET /cascade/{id} handler.
func (s *Server) registerCascadeRoute() error {
	s.mux.HandleFunc("GET /cascade/{id}", func(w http.ResponseWriter, req *http.Request) {
		if s.cascadeDetailRenderer == nil {
			http.Error(w, "internal server error: cascade detail renderer not configured", http.StatusInternalServerError)
			return
		}
		err := s.cascadeDetailRenderer.RenderCascadeDetail(req.Context(), w, req)
		if err != nil {
			var notFoundErr *NotFoundError
			if errors.As(err, &notFoundErr) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	})
	return nil
}
