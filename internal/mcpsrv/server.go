package mcpsrv

import (
	"context"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/server"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/ops"
)

// Config configures the MCP server's runtime behavior.
//
// Post-V2-PLAN §12.11 / §14.9: ProjectPath is REQUIRED. MCP clients
// supply it via the stdio handshake wrapper at CLI boot time; direct
// library callers must supply it explicitly.
type Config struct {
	Name    string
	Version string
	// ProjectPath is the absolute project directory. Required. New
	// pre-warms the schema cache for this project; a malformed
	// schema aborts construction. A missing schema (ErrNoSchema) is
	// tolerated so a fresh project — not yet `ta init`'d — can still
	// start the server and fail per-tool-call with a loud "no
	// schema" error.
	ProjectPath string
}

// Server wraps the MCP server with ta-specific dependencies.
type Server struct {
	cfg Config
	srv *server.MCPServer
}

// New constructs an MCP server configured with ta's data and schema
// tools: get / list_sections / create / update / delete / schema /
// search. Upsert is retired per V2-PLAN §10.1 (hard cut, no alias).
//
// cfg.ProjectPath is required. New pre-warms the schema cache and
// surfaces any malformed-schema error immediately so the MCP client
// sees a clean startup failure rather than per-call errors. A project
// that has not yet been initialized (no .ta/schema.toml) is tolerated
// at startup — individual tool calls will surface ErrNoSchema when
// they try to read.
func New(cfg Config) (*Server, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("mcpsrv: Config.Name is required")
	}
	if cfg.Version == "" {
		return nil, fmt.Errorf("mcpsrv: Config.Version is required")
	}
	if cfg.ProjectPath == "" {
		return nil, fmt.Errorf("mcpsrv: Config.ProjectPath is required")
	}
	if _, err := ops.ResolveProject(cfg.ProjectPath); err != nil {
		if !errors.Is(err, config.ErrNoSchema) {
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
