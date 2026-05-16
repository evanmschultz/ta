package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/render"
)

// newListSectionsCmd mirrors the MCP tool `list_sections`. The CLI
// takes a project directory via `--path` (default cwd) plus an optional
// id-prefix scope (either `--scope <value>` or a second positional —
// not both). Output emits full project-level ids so copy-paste composes
// with `get` / `update` / `delete`. `--limit <N>` (default 10, `-n`
// shorthand) and `--all` control the cap; they are mutually exclusive.
func newListSectionsCmd() *cobra.Command {
	var asJSON bool
	var scope string
	var limit int
	var all bool
	cmd := &cobra.Command{
		Use:   "list-sections [scope]",
		Short: "Enumerate record ids under a scope; mirrors MCP tool `list_sections`.",
		Long: "Walks every record in scope and emits its full id (e.g. " +
			"`plans.demo-1`). Scope is an optional id prefix supplied via " +
			"--scope or as the positional argument; omitted = whole " +
			"project. --limit caps the list (default 10, -n shorthand); " +
			"--all returns every match. --path defaults to cwd; relative " +
			"or absolute accepted.",
		Example: `  ta list-sections
  ta list-sections plans
  ta list-sections --scope plans.todo-
  ta list-sections --scope plans --all --json`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			return runWithJSONErrEnvelope(c, asJSON, func() error {
				path, err := resolveCLIPath(c)
				if err != nil {
					return err
				}
				resolvedScope, err := resolveListScope(scope, args)
				if err != nil {
					return err
				}
				sections, err := ops.ListSections(path, resolvedScope, limit, all)
				if err != nil {
					return err
				}
				// Post-fetch slice removed — endpoint owns the cap per
				// docs/PLAN.md §12.17.5 [A2.1] and the §6a.1 decoupling
				// principle. CLI flags pass through verbatim.
				if asJSON {
					if sections == nil {
						sections = []string{}
					}
					enc := json.NewEncoder(c.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]any{"sections": sections})
				}
				title := path
				if resolvedScope != "" {
					title = path + " [scope: " + resolvedScope + "]"
				}
				return render.New(c.OutOrStdout()).List(title, sections, "(no sections)")
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().StringVar(&scope, "scope", "", "id prefix to enumerate (e.g. `plans` or `plans.todo-`)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "cap the list at N ids (default 10)")
	cmd.Flags().BoolVar(&all, "all", false, "return every match (disables --limit)")
	cmd.MarkFlagsMutuallyExclusive("limit", "all")
	addPathFlag(cmd)
	return cmd
}

// resolveListScope reconciles the `--scope` flag with the optional
// positional scope argument. The positional is a convenience for
// --scope; supplying both forms at once is ambiguous and errors. Empty
// scope (neither form set) means "whole project" and is returned as "".
func resolveListScope(flagScope string, args []string) (string, error) {
	var positional string
	if len(args) == 1 {
		positional = args[0]
	}
	switch {
	case flagScope != "" && positional != "":
		return "", fmt.Errorf("pass scope once: supply either the positional or --scope, not both")
	case flagScope != "":
		return flagScope, nil
	default:
		return positional, nil
	}
}
