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
}

// Server wraps the MCP server with ta-specific dependencies.
type Server struct {
	cfg Config
	srv *server.MCPServer
}

// New constructs an MCP server configured with ta's data and schema
// tools: get / list_sections / create / update / delete / schema.
// Upsert is retired per V2-PLAN §10.1 (hard cut, no alias).
func New(cfg Config) (*Server, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("mcpsrv: Config.Name is required")
	}
	if cfg.Version == "" {
		return nil, fmt.Errorf("mcpsrv: Config.Version is required")
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
