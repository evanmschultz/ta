package mcpsrv

import (
	"context"

	"github.com/mark3labs/mcp-go/server"
)

// Config configures the MCP server's runtime behavior.
type Config struct {
	Name    string
	Version string
}

// Server wraps the MCP server with ta-specific dependencies.
type Server struct {
	cfg Config
	srv *server.MCPServer
}

// New constructs an MCP server configured with ta's three tools. Phase 6
// fills in tool registration; this stub anchors the mcp-go dependency.
func New(cfg Config) *Server {
	srv := server.NewMCPServer(cfg.Name, cfg.Version)
	return &Server{cfg: cfg, srv: srv}
}

// Run serves MCP over stdio until ctx is cancelled or the transport closes.
func (s *Server) Run(ctx context.Context) error {
	_ = ctx
	return server.ServeStdio(s.srv)
}
