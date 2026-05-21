// Package server provides the HTTP cascade browser server: config bootstrap,
// mux construction, and route registration surface.
//
// L2-B scope: listener config, mux construction, route registration, path
// parsing, and handler-to-view delegation for /, /cascade/<id>, /roadmap,
// /schema, and /search. Route handlers delegate record loading/rendering to
// an injected view layer instead of reaching into internal/ops directly.
//
// This package is intentionally thin: it owns the HTTP machinery but not the
// business logic. Later route droplets attach handlers to the mux without
// changing the constructor seam.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

// Config holds the HTTP listener configuration. It is the L2-A → L2-B
// delegation contract: cmd/ta's serve subcommand populates Bind and Port,
// passing them to Server.New() to boot the listener.
type Config struct {
	Bind string // HTTP listener bind address (e.g. "127.0.0.1", "0.0.0.0")
	Port int    // HTTP listener port (e.g. 4321)
}

// Server is the HTTP cascade browser server. It holds the config, mux, and
// any future view-layer dependencies. Route registration happens through
// exported methods that attach handlers to the mux without reshaping the
// constructor.
type Server struct {
	config                 Config
	mux                    *http.ServeMux
	viewRenderer           ViewRenderer
	cascadeDetailRenderer  CascadeDetailRenderer
}

// New constructs a new Server with the given config and a base mux.
// The mux is empty at construction time; route droplets attach handlers
// via the mux without changing this seam.
func New(cfg Config) *Server {
	mux := http.NewServeMux()
	s := &Server{
		config: cfg,
		mux:    mux,
	}
	// Register routes early. Route handlers may be added by droplets
	// using methods like registerRootRoute.
	_ = s.registerRootRoute()
	_ = s.registerCascadeRoute()
	return s
}

// Run starts the HTTP listener on the configured bind address and port,
// blocking until ctx is canceled or the listener encounters a fatal error.
//
// This is a placeholder that will be extended by route droplets to register
// handlers before Listen actually fires. For now, it returns immediately
// after a diagnostic message.
func (s *Server) Run(ctx context.Context) error {
	addr := net.JoinHostPort(s.config.Bind, strconv.Itoa(s.config.Port))
	// TODO: extend with route registration from L2-B droplets.
	return fmt.Errorf("server.Run(%q): placeholder (awaiting L2-B route registration)", addr)
}
