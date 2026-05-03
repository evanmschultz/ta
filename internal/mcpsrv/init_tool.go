package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/evanmschultz/ta/internal/initapply"
)

// initTool exposes the F24 multi-category init flow over MCP.
// Two actions:
//
//   - preview — list available items per category, no disk writes.
//   - apply   — execute a Selections payload against `target`.
//
// The Selections shape mirrors the CLI `--selections-file` JSON
// exactly so script + agent surfaces share one payload (F24 lock
// #4).
func initTool() mcp.Tool {
	return mcp.NewTool(
		"init",
		mcp.WithDescription(
			"Multi-category bootstrap. action=preview returns the items "+
				"available across binary `examples/` and home `~/.ta/`; "+
				"action=apply takes a Selections payload and writes the "+
				"selected items into target. on_conflict picks the "+
				"resolution policy when destinations already exist.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute). Used as the default target when 'target' is omitted.")),
		mcp.WithString("action", mcp.Description("One of preview | apply. Defaults to preview.")),
		mcp.WithString("target", mcp.Description("Optional override of 'path' for the destination. Use $HOME/.ta to bootstrap the home library.")),
		mcp.WithObject(
			"selections",
			mcp.Description("action=apply only. {schemas, agents, configs, docs-templates, on_conflict}. See CLI --selections-file. "+
				"Each entry accepts either a bare-string name (legacy: home-then-binary fallback) "+
				"or an object {name, provenance} where provenance ∈ {ta, home, \"\"} pins the source. "+
				"Use {name, provenance: \"ta\"} to force the binary-shipped fragment when a home item shares the name.\n"+
				"  schemas:        [\"<db>\" | {name, provenance}, ...]\n"+
				"  agents:         [{group, name, provenance?}, ...]\n"+
				"  configs:        [\"<filename>\" | {name, provenance}, ...]\n"+
				"  docs-templates: [\"<canonical>\" | {name, provenance}, ...]\n"+
				"  on_conflict:    error|skip|overwrite|force"),
			mcp.AdditionalProperties(map[string]any{}),
		),
		mcp.WithString("on_conflict", mcp.Description("Conflict policy override (CLI flag equivalent). Beats selections.on_conflict when set.")),
	)
}

func handleInit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
	}
	target := req.GetString("target", "")
	if target == "" {
		target = path
	}
	action := req.GetString("action", "preview")
	switch action {
	case "preview":
		report, err := initapply.Preview(target)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mustJSON(report), nil
	case "apply":
		args := req.GetArguments()
		var sel initapply.Selections
		if raw, ok := args["selections"]; ok && raw != nil {
			data, err := json.Marshal(raw)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("encode selections: %v", err)), nil
			}
			sel, err = initapply.SelectionsFromJSON(data)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("parse selections: %v", err)), nil
			}
		}
		policyStr := req.GetString("on_conflict", "")
		if policyStr == "" {
			policyStr = sel.OnConflict
		}
		policy, err := initapply.ParsePolicy(policyStr)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		report, err := initapply.Apply(target, sel, policy)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Surface conflicts loudly on policy=error so MCP callers
		// see the same loud failure mode the CLI does.
		if policy == initapply.PolicyError {
			conflicts := initapply.AggregateConflicts(report)
			if len(conflicts) > 0 {
				return mcp.NewToolResultError(fmt.Sprintf(
					"init: %d conflict(s); re-run with on_conflict=skip|overwrite|force: %s",
					len(conflicts), strings.Join(conflicts, ", "),
				)), nil
			}
		}
		return mustJSON(report), nil
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q (want preview|apply)", action)), nil
	}
}
