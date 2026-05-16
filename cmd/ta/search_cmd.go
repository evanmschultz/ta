package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/render"
)

// newSearchCmd mirrors the MCP tool `search` (V2-PLAN §3.7 / §7). The
// CLI renders hits as one laslig card per record with the string fields
// glamour-rendered per §13.1 / §13.2. With --json the laslig path is
// bypassed and a structured hit array is written for agent consumption.
func newSearchCmd() *cobra.Command {
	var scope string
	var matchJSON string
	var query string
	var field string
	var typeName string
	var asJSON bool
	var limit int
	var all bool
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Structured + regex search across records; mirrors MCP tool `search`.",
		Long: "Walks declared records under --scope, applies --match exact-match " +
			"filters on typed scalar fields (JSON object), then optionally " +
			"applies --query regex against string fields (restricted to " +
			"--field when set). One laslig card per hit — or, with --json, " +
			"a structured hits array for agent consumption. --limit caps the " +
			"hit count (default 10, -n shorthand); --all returns every match. " +
			"--path defaults to cwd; relative or absolute accepted.",
		Example: "  ta search --scope=plans --match '{\"status\":\"todo\"}'\n" +
			"  ta search --path /abs/proj --scope=plans --query='TODO' --field=body\n" +
			"  ta search --scope=plans --all --json",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			return runWithJSONErrEnvelope(c, asJSON, func() error {
				path, err := resolveCLIPath(c)
				if err != nil {
					return err
				}
				var match map[string]any
				if matchJSON != "" {
					if err := json.Unmarshal([]byte(matchJSON), &match); err != nil {
						return fmt.Errorf("parse --match JSON: %w", err)
					}
				}
				hits, err := ops.Search(path, scope, typeName, match, query, field, limit, all)
				if err != nil {
					return err
				}
				if asJSON {
					return emitSearchJSON(c.OutOrStdout(), hits)
				}
				return renderSearchHits(c.OutOrStdout(), path, hits)
			})
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "id prefix to narrow traversal (e.g. `plans` or `plans.todo-`)")
	cmd.Flags().StringVar(&matchJSON, "match", "", "JSON object of {field: exact-value}")
	cmd.Flags().StringVar(&query, "query", "", "Go RE2 regex matched against string fields")
	cmd.Flags().StringVar(&field, "field", "", "restrict --query to one string field")
	cmd.Flags().StringVar(&typeName, "type", "", "optional db-qualified type (`<db>.<type>`); post-walk filter against the index entry for each hit's id")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "cap the hit count at N (default 10)")
	cmd.Flags().BoolVar(&all, "all", false, "return every match (disables --limit)")
	cmd.MarkFlagsMutuallyExclusive("limit", "all")
	addPathFlag(cmd)
	return cmd
}

// emitSearchJSON writes the --json form of `search`. Shape:
// {"hits": [{"id": "...", "bytes": "...", "fields": {...}}]}.
func emitSearchJSON(w io.Writer, hits []ops.SearchHit) error {
	out := make([]map[string]any, len(hits))
	for i, h := range hits {
		out[i] = map[string]any{
			"id":     h.ID,
			"bytes":  string(h.Bytes),
			"fields": h.Fields,
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"hits": out})
}

func renderSearchHits(w io.Writer, path string, hits []ops.SearchHit) error {
	r := render.New(w)
	if len(hits) == 0 {
		return r.Notice(laslig.NoticeInfoLevel, "search", "no hits", nil)
	}
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return fmt.Errorf("resolve schema: %w", err)
	}
	for _, hit := range hits {
		_, typeSt, err := lookupDBAndType(resolution.Registry, path, hit.ID)
		if err != nil {
			// Best-effort: render without typed fields.
			if err := r.Record(hit.ID, nil); err != nil {
				return err
			}
			continue
		}
		if err := r.Record(hit.ID, render.BuildFields(typeSt, hit.Fields)); err != nil {
			return err
		}
	}
	return nil
}
