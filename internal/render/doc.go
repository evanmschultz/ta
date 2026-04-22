// Package render wraps laslig with a ta-specific Renderer so every CLI
// subcommand shares one output contract (V2-PLAN §13). Mutating actions
// emit concise Notice banners; read actions (`get`, `list_sections`,
// `schema get`, `search`) emit structured blocks or glamour-rendered
// markdown. String-typed fields are rendered through laslig's Markdown
// path per §13.2 — plain text round-trips unchanged, markdown content
// picks up syntax-highlighted code blocks and headings.
//
// This package is CLI-only. `internal/mcpsrv/` MUST NOT import it; the
// dependency firewall keeps MCP responses strictly structured JSON
// (§13.3). `cmd/ta/commands.go` wires every subcommand through a
// Renderer constructed at call time with the subcommand's writer.
package render
