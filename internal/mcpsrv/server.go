package mcpsrv

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/server"
)

// Config configures the MCP server's runtime behavior.
type Config struct {
	Name    string
	Version string
	// ProjectPath, when non-empty, triggers startup meta-validation:
	// New resolves the schema cascade for the given project directory
	// via the shared cache and returns any resolve-time error before
	// the server begins serving. This turns a malformed cascade into
	// a startup failure instead of a per-call ValidationError every
	// client sees. Leave empty to skip the pre-warm — CLI and test
	// harnesses that construct the server without a fixed project
	// directory rely on this zero-value behavior.
	//
	// Per V2-PLAN §12.9: "startup meta-validation refuses to boot on
	// a malformed cascade."
	ProjectPath string
}

// Server wraps the MCP server with ta-specific dependencies.
type Server struct {
	cfg Config
	srv *server.MCPServer
}

// New constructs an MCP server configured with ta's data and schema
// tools: get / list_sections / create / update / delete / schema.
// Upsert is retired per V2-PLAN §10.1 (hard cut, no alias).
//
// When cfg.ProjectPath is set, New pre-warms the schema cache for
// that project and surfaces any cascade-resolve error immediately.
// A malformed cascade aborts construction so the MCP client sees a
// clean startup failure rather than per-call errors.
func New(cfg Config) (*Server, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("mcpsrv: Config.Name is required")
	}
	if cfg.Version == "" {
		return nil, fmt.Errorf("mcpsrv: Config.Version is required")
	}
	if cfg.ProjectPath != "" {
		if _, err := defaultCache.Resolve(cfg.ProjectPath); err != nil {
			return nil, fmt.Errorf("mcpsrv: startup schema pre-warm for %s: %w", cfg.ProjectPath, err)
		}
	}
	srv := server.NewMCPServer(cfg.Name, cfg.Version)
	s := &Server{cfg: cfg, srv: srv}
	s.registerTools()
	return s, nil
}

// Run serves MCP over stdio until the transport closes.
func (s *Server) Run(ctx context.Context) error {
	_ = ctx
	return server.ServeStdio(s.srv)
}

func (s *Server) registerTools() {
	s.srv.AddTool(getTool(), handleGet)
	s.srv.AddTool(listSectionsTool(), handleListSections)
	s.srv.AddTool(createTool(), handleCreate)
	s.srv.AddTool(updateTool(), handleUpdate)
	s.srv.AddTool(deleteTool(), handleDelete)
	s.srv.AddTool(schemaTool(), handleSchema)
	s.srv.AddTool(searchTool(), handleSearch)
}
