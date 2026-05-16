package mcpsrv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// path_guard.go — MCP path-arg cwd-confinement guard.
//
// Every MCP handler in this package accepts a `path` string argument
// that becomes the project-resolution root for the call. Pre-H4 the
// `path` was forwarded straight into ops/initapply with no
// normalization or containment check, so an MCP client could reach
// any absolute path the ta process can see. The MCP-server
// invariant — one project per process, pinned at construction via
// cfg.ProjectPath — was advisory rather than enforced.
//
// H4 wires a single chokepoint: registerTools sets the package-level
// projectRoot once from cfg.ProjectPath, and every handler routes
// its `path` arg through guardPath. The helper runs
// filepath.Abs + filepath.Clean defensively (so `./foo/../bar`
// normalizes to `bar` BEFORE the containment check fires), then
// refuses unless the result equals projectRoot or is a descendant.
//
// Escape hatch: TA_MCP_PATH_GUARD=off disables the containment
// check while still running Abs+Clean. Any other value (unset,
// empty, "on", "1", garbage) leaves the guard ON. See
// docs/security/mcp_path_arg.md for the threat model and the
// scenarios that justify the env override.

var (
	// projectRoot is the absolute, cleaned project directory the
	// server was constructed with. Set once at registerTools time.
	// Reads are stable for the lifetime of the process — there is no
	// concurrent mutation, so no mutex is needed.
	projectRoot string

	// guardDisabled is the TA_MCP_PATH_GUARD=off escape hatch.
	// Read once at package init so toggling the env at runtime has
	// no effect — operators set the policy at process start.
	guardDisabled bool
)

func init() {
	guardDisabled = strings.EqualFold(strings.TrimSpace(os.Getenv("TA_MCP_PATH_GUARD")), "off")
}

// setProjectRootForGuard stashes the server's project root for
// guardPath to compare against. Called from registerTools. The
// input is normalized through filepath.Abs+Clean so the stored
// value matches the shape guardPath produces for incoming
// requests; any error in normalization (e.g. cwd lookup failure)
// is swallowed and the raw value is stored — the guard will still
// reject anything that does not match, just with a less canonical
// comparison.
func setProjectRootForGuard(p string) {
	if p == "" {
		projectRoot = ""
		return
	}
	if abs, err := filepath.Abs(p); err == nil {
		projectRoot = filepath.Clean(abs)
		return
	}
	projectRoot = filepath.Clean(p)
}

// guardPath normalizes rawPath via filepath.Abs+filepath.Clean and
// refuses it if it escapes projectRoot. Returns the cleaned
// absolute path on accept. The cleaning step runs unconditionally —
// even with guardDisabled=true — so callers always get a canonical
// path to forward downstream.
//
// projectRoot empty (server not constructed via the standard path,
// or library caller importing the package) is treated as
// "guard not configured" and falls through to Abs+Clean only.
// This is fail-open by design for non-server callers; the
// server hot path always has projectRoot set via registerTools.
func guardPath(rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("path arg is required")
	}
	abs, err := filepath.Abs(rawPath)
	if err != nil {
		return "", fmt.Errorf("normalize path %q: %w", rawPath, err)
	}
	cleaned := filepath.Clean(abs)

	if guardDisabled || projectRoot == "" {
		return cleaned, nil
	}

	if cleaned == projectRoot {
		return cleaned, nil
	}
	// Descendant check via prefix-with-separator. filepath.Clean
	// strips trailing separators so projectRoot does not already
	// end in one; appending it once gives the exact "X is inside
	// Y" semantics without confusing `/foo/bar` with `/foo/barbaz`.
	sep := string(filepath.Separator)
	if strings.HasPrefix(cleaned, projectRoot+sep) {
		return cleaned, nil
	}

	return "", fmt.Errorf(
		"path %q is outside the MCP server's project root %q; "+
			"set TA_MCP_PATH_GUARD=off to disable this check",
		cleaned, projectRoot,
	)
}

// guardedPathArg is the one-line replacement every handler uses
// in place of the prior `path, err := req.RequireString("path")` +
// inline error-return shape. On success returns the cleaned,
// guarded absolute path and a nil error-result. On any failure
// (missing arg, normalization failure, or containment violation)
// returns "" and a ready-to-return *mcp.CallToolResult carrying
// the error text — mirrors the pre-H4 error shape ("invalid path
// arg: …") for the RequireString branch and adds a distinct
// "outside project root" branch for the guard branch.
func guardedPathArg(req mcp.CallToolRequest) (string, *mcp.CallToolResult) {
	raw, err := req.RequireString("path")
	if err != nil {
		return "", mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err))
	}
	guarded, gerr := guardPath(raw)
	if gerr != nil {
		return "", mcp.NewToolResultError(gerr.Error())
	}
	return guarded, nil
}
