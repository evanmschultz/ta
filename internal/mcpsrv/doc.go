// Package mcpsrv is the MCP protocol glue over internal/ops. It hosts
// the MCP server, declares the tool surface (get, list_sections, create,
// update, delete, search, schema), and adapts each handler to the
// plain-Go endpoints in internal/ops. See docs/PLAN.md §6a.
package mcpsrv
