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

// NotFoundRenderer is the consumer-side interface for not-found page rendering.
type NotFoundRenderer interface {
	RenderNotFound(w http.ResponseWriter, req *http.Request, missingID string) error
}

// WithCascadeDetailRenderer sets the cascade detail renderer dependency.
func (s *Server) WithCascadeDetailRenderer(cdr CascadeDetailRenderer) *Server {
	if cdr != nil {
		s.cascadeDetailRenderer = cdr
	}
	return s
}

// WithNotFoundRenderer sets the not-found renderer dependency.
func (s *Server) WithNotFoundRenderer(nfr NotFoundRenderer) *Server {
	if nfr != nil {
		s.notFoundRenderer = nfr
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
				// Delegate missing-id rendering to the not-found renderer.
				if s.notFoundRenderer != nil {
					id := req.PathValue("id")
					renderErr := s.notFoundRenderer.RenderNotFound(w, req, id)
					if renderErr != nil {
						http.Error(w, "internal server error", http.StatusInternalServerError)
					}
					return
				}
				// Fallback if not-found renderer is not configured.
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	})
	return nil
}
