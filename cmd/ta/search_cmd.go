package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	// Blank imports register the html / md / txt Format engines with the
	// format substrate. L3-D5-D2 mirrors D5-D1's pattern (see get_cmd.go):
	// each read-side command file anchors its own backend registration so
	// the dependency stays explicit per-callsite.
	_ "github.com/evanmschultz/ta/internal/backend/html"
	_ "github.com/evanmschultz/ta/internal/backend/md_explicit"
	_ "github.com/evanmschultz/ta/internal/backend/txt"

	"github.com/evanmschultz/ta/internal/format"
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
	var asFormat string
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Structured + regex search across records; mirrors MCP tool `search`.",
		Long: "Walks declared records under --scope, applies --match exact-match " +
			"filters on typed scalar fields (JSON object), then optionally " +
			"applies --query regex against string fields (restricted to " +
			"--field when set). Query may be supplied as the positional " +
			"argument or via --query; passing both is an error. One laslig " +
			"card per hit — or, with --json, a structured hits array for " +
			"agent consumption. --limit caps the hit count (default 10, -n " +
			"shorthand); --all returns every match. --path defaults to cwd; " +
			"relative or absolute accepted.",
		Example: "  ta search --scope=plans --match '{\"status\":\"todo\"}'\n" +
			"  ta search 'TODO' --scope=plans --field=body\n" +
			"  ta search --path /abs/proj --scope=plans --query='TODO' --field=body\n" +
			"  ta search --scope=plans --all --json",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			return runWithJSONErrEnvelope(c, asJSON, func() error {
				path, err := resolveCLIPath(c)
				if err != nil {
					return err
				}
				resolvedQuery, err := resolveSearchQuery(args, query)
				if err != nil {
					return err
				}
				var match map[string]any
				if matchJSON != "" {
					if err := json.Unmarshal([]byte(matchJSON), &match); err != nil {
						return fmt.Errorf("parse --match JSON: %w", err)
					}
				}
				hits, err := ops.Search(path, scope, typeName, match, resolvedQuery, field, limit, all)
				if err != nil {
					return err
				}
				// L3-D5-D2: when --as is set, route every hit's body
				// through format.Get(<name>).Marshal before emit. The
				// dispatch fires for both ANSI and JSON output surfaces
				// so the rendered/encoded bytes per hit are consistent.
				if asFormat != "" {
					return runSearchWithFormat(c.OutOrStdout(), path, hits, asFormat, asJSON)
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
	// L3-D5-D2: --as mirrors L3-D5-D1's flag on `ta get`. NO --template
	// per planner routed concern #6 (search-hit scope makes manifest
	// selection per-hit ambiguous in the same way batch get rejects it).
	cmd.Flags().StringVar(&asFormat, "as", "", "format engine name (html | md | txt); routes each hit body through format.Get(name).Marshal before emit (default: db's on-disk format)")
	cmd.MarkFlagsMutuallyExclusive("limit", "all")
	addPathFlag(cmd)
	return cmd
}

// runSearchWithFormat is the L3-D5-D2 sibling of get_cmd.go's
// runGetSingleWithFormat. For each search hit, the steps mirror D5-D1:
//  1. Resolve the hit's db.Format via dbFormatFor(path, hit.ID).
//  2. Compute effectiveAs: explicit asFormat, or default to string(db.Format).
//  3. Mismatch check: if asFormat != "" && asFormat != string(db.Format)
//     → planner-pinned "db.Format=<x>; --as=<y> requires matching format".
//     The check fires per-hit because a single search can span multiple
//     dbs with heterogeneous formats; one mismatch fails fast for the
//     whole call (rather than partial output).
//  4. Resolve the format engine via format.Get(<name>); unknown name
//     wraps the registry error.
//  5. Parse(hit.Bytes, nil) → Marshal(blocks, nil) → outBytes. No
//     manifest is loaded — --template is not supported for search per
//     the planner routed concern.
//
// Hit order is preserved: hits are iterated in the order ops.Search
// returned them, and emitted in that order to stdout. The JSON path
// emits a {"hits": [{id, bytes, fields}]} envelope mirroring
// emitSearchJSON; ANSI path emits one laslig record block per hit's
// formatted body.
func runSearchWithFormat(w io.Writer, path string, hits []ops.SearchHit, asFormat string, asJSON bool) error {
	type outHit struct {
		ID     string         `json:"id"`
		Bytes  string         `json:"bytes"`
		Fields map[string]any `json:"fields,omitempty"`
	}
	out := make([]outHit, 0, len(hits))
	for _, hit := range hits {
		dbFormat, err := dbFormatFor(path, hit.ID)
		if err != nil {
			return err
		}
		effectiveAs := asFormat
		if effectiveAs == "" {
			effectiveAs = string(dbFormat)
		}
		if asFormat != "" && asFormat != string(dbFormat) {
			return fmt.Errorf("db.Format=%s; --as=%s requires matching format", string(dbFormat), asFormat)
		}
		engine, err := format.Get(effectiveAs)
		if err != nil {
			return fmt.Errorf("ta search: --as: %w", err)
		}
		blocks, err := engine.Parse(hit.Bytes, nil)
		if err != nil {
			return fmt.Errorf("ta search: parse: %w", err)
		}
		marshaled, err := engine.Marshal(blocks, nil)
		if err != nil {
			return fmt.Errorf("ta search: marshal: %w", err)
		}
		out = append(out, outHit{
			ID:     hit.ID,
			Bytes:  string(marshaled),
			Fields: hit.Fields,
		})
	}
	if asJSON {
		shaped := make([]map[string]any, len(out))
		for i, h := range out {
			shaped[i] = map[string]any{
				"id":     h.ID,
				"bytes":  h.Bytes,
				"fields": h.Fields,
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"hits": shaped})
	}
	r := render.New(w)
	if len(out) == 0 {
		return r.Notice(laslig.NoticeInfoLevel, "search", "no hits", nil)
	}
	for _, h := range out {
		if err := r.Markdown(h.Bytes); err != nil {
			return err
		}
	}
	return nil
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

// resolveSearchQuery reconciles the `--query` flag with the optional
// positional query argument. The positional is a convenience for
// --query; supplying both forms at once is ambiguous and errors. Empty
// query (neither form set) means "no regex filter" and is returned as
// "" — ops.Search short-circuits the regex compile in that case.
//
// Byte-mirrors resolveListScope at cmd/ta/list_sections_cmd.go:86-99
// per the L3-G4 planner contract; the helper deliberately stays in
// search_cmd.go to firewall the L3-G6 commands.go promotion concern.
func resolveSearchQuery(args []string, flagQuery string) (string, error) {
	var positional string
	if len(args) == 1 {
		positional = args[0]
	}
	switch {
	case flagQuery != "" && positional != "":
		return "", fmt.Errorf("pass query once: supply either the positional or --query, not both")
	case flagQuery != "":
		return flagQuery, nil
	default:
		return positional, nil
	}
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
