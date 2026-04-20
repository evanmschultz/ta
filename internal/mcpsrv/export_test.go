package mcpsrv

import "github.com/mark3labs/mcp-go/server"

// MCPServer exposes the underlying mcp-go server for in-process test clients.
// Test-only; do not call from non-test code.
func (s *Server) MCPServer() *server.MCPServer { return s.srv }
