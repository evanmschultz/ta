package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/render"
)

// newGetCmd mirrors the MCP tool `get`. Without --fields the CLI
// synthesizes every declared field from the record and routes through
// the shared render.Renderer.Record helper — same visual shape as `ta
// search` hits, and the same dispatch `ta get --fields <list>` already
// uses (V2-PLAN §12.17.5 [B3]). With --fields the named field values
// are rendered per type. With --json the laslig path is bypassed;
// structured JSON is written for agent consumption (V2-PLAN §14.3).
//
// An id prefix (e.g. `plans` to enumerate every record in plans.toml)
// returns every matching record in file-parse order; --limit (default
// 10, -n shorthand) and --all control the cap. Full ids silently ignore
// --limit / --all.
//
// F37 universal items[] shape: positional N≥1 ids are the same-payload
// shorthand; --batch FILE|- reads {"items": [{id, fields?}, ...]} JSON
// for heterogeneous reads. Length 1 still routes through the existing
// scope-vs-single dispatch so id-prefix expansion stays intact. Length
// ≥2 is single-record-per-item only — a scope-prefix in batch mode
// would break the per-item single-result contract.
func newGetCmd() *cobra.Command {
	var fields []string
	var asJSON bool
	var limit int
	var all bool
	var typeName string
	var batch string
	cmd := &cobra.Command{
		Use:   "get <id> [<id>...]",
		Short: "Read one or more records by id, or every record under an id prefix; optionally extract declared field values",
		Long: "Mirrors the MCP tool `get`. A full id (e.g. `plans.demo-1`) " +
			"returns one record; without --fields every declared field is " +
			"rendered through the shared per-field helper (string fields as " +
			"markdown, scalars as label:value, arrays/tables as fenced JSON); " +
			"with --fields name[,name...] the named subset is rendered. An " +
			"id prefix (e.g. `plans` for every record in `plans.toml`) " +
			"returns every matching record in file-parse order as a sequence " +
			"of laslig Section blocks, or --json " +
			"{\"records\":[{id, fields}, ...]}. --limit (default 10, -n " +
			"shorthand) and --all control the cap for id-prefix scopes; both " +
			"are silently ignored for full-id reads and are mutually " +
			"exclusive. F37: pass N≥2 positional ids (same --fields applied " +
			"to each) or --batch FILE|- for heterogeneous reads — duplicate " +
			"ids are allowed (idempotent reads return the record twice). " +
			"With --json the laslig path is bypassed and JSON is written " +
			"for agent consumption. --path defaults to cwd; relative or " +
			"absolute accepted.",
		Example: "  ta get plans.task-001\n" +
			"  ta get --path /abs/proj plans.task-001 --fields status,body\n" +
			"  ta get plans.task-001 plans.task-002 plans.task-003 --json\n" +
			"  ta get --batch reads.json --json\n" +
			"  ta get plans --all --json\n" +
			"  ta get plans --limit 5",
		Args:          cobra.MinimumNArgs(0),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			return runWithJSONErrEnvelope(c, asJSON, func() error {
				path, err := resolveCLIPath(c)
				if err != nil {
					return err
				}
				if batch != "" && len(args) > 0 {
					return errors.New("ta get: use either positional ids or --batch, not both")
				}
				// Length-1 single-positional path preserves the pre-F37
				// scope-vs-single dispatch so id-prefix expansion still works
				// for `ta get plans` and friends. F37 batch semantics only
				// kick in when the user opts into multi-id or --batch.
				if batch == "" && len(args) == 1 {
					return runGetSingle(c, path, args[0], typeName, fields, limit, all, asJSON)
				}
				items, err := collectGetItems(c.InOrStdin(), batch, args, fields)
				if err != nil {
					return err
				}
				if err := validateGetItems(items); err != nil {
					return err
				}
				results := runGetItems(path, items)
				if err := emitGetBatch(c.OutOrStdout(), path, results, asJSON); err != nil {
					return err
				}
				// Misses (found=false) are NOT a CLI failure for reads —
				// they're a normal observable state. Genuine per-item
				// errors (malformed id, schema resolve failure, IO issue)
				// DO escalate to a non-zero exit so operators see them.
				//
				// In --json mode the per-item errors are already encoded
				// inside the {results} envelope written by emitGetBatch
				// above; surfacing a synthetic err here would feed
				// runWithJSONErrEnvelope a SECOND `{"error": ...}` envelope
				// on stdout, breaking the JSON stream. Plain-text mode
				// still escalates so operators see the non-zero exit.
				if anyGetErrored(results) && !asJSON {
					return errors.New("ta get: one or more items errored")
				}
				return nil
			})
		},
	}
	cmd.Flags().StringSliceVar(&fields, "fields", nil, "comma-separated declared field names to extract")
	cmd.Flags().StringSliceVar(&fields, "field", nil, "declared field name to extract (repeatable)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "cap the record count at N when <id> is a prefix (default 10; ignored for full ids; mutually exclusive with --all)")
	cmd.Flags().BoolVar(&all, "all", false, "return every record when <id> is a prefix (ignored for full ids; mutually exclusive with --limit)")
	cmd.Flags().StringVar(&typeName, "type", "", "optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id")
	cmd.Flags().StringVar(&batch, "batch", "", "read {\"items\":[{id, fields?}, ...]} JSON from FILE (or `-` for stdin); mutually exclusive with positional ids")
	cmd.MarkFlagsMutuallyExclusive("limit", "all")
	addPathFlag(cmd)
	return cmd
}

// runGetSingle is the pre-F37 single-positional path: one id, with
// scope-vs-record dispatch and per-fields rendering preserved for back-
// compat with existing `ta get plans.foo` and `ta get plans` flows.
//
// L3-C2-D1 adds a group-prefix gate between the scope check and the
// single-record path. When ops.GetGroup succeeds the id names a group
// prefix and runGetGroup emits the aggregate output. When GetGroup
// returns ops.ErrNoGroup (no children under the prefix) or
// ops.ErrIndexMissing (no index present), the gate falls through to
// the existing single-record path. Any other error from GetGroup
// surfaces immediately.
func runGetSingle(c *cobra.Command, path, id, typeName string, fields []string, limit int, all bool, asJSON bool) error {
	isScope, err := ops.IsScopeAddress(path, id)
	if err != nil {
		return err
	}
	if isScope {
		return runGetScope(c, path, id, fields, limit, all, asJSON)
	}

	// Group-prefix gate: try GetGroup before the single-record path.
	// GetGroup loads the index internally and returns ErrNoGroup when id
	// is not a group prefix (IsGroupPrefix would return false). We treat
	// ErrIndexMissing as a graceful fall-through so single-record gets
	// work even when the index has not been initialised.
	records, groupErr := ops.GetGroup(path, id, fields, limit, all)
	if groupErr == nil {
		return runGetGroup(c, records, id, asJSON)
	}
	if !errors.Is(groupErr, ops.ErrNoGroup) && !errors.Is(groupErr, ops.ErrIndexMissing) {
		// An unexpected error from the group path (IO, schema, per-child
		// read failure, etc.) surfaces immediately rather than silently
		// falling through to a single-record attempt that would almost
		// certainly produce a different but equally confusing error.
		return groupErr
	}
	// ErrNoGroup or ErrIndexMissing: id is not a group prefix (or index
	// absent) — continue to the existing single-record path below.

	if asJSON {
		res, err := ops.Get(path, id, typeName, fields)
		if err != nil {
			return err
		}
		return emitGetJSON(c.OutOrStdout(), id, res.Bytes, res.Fields, len(fields) > 0)
	}
	r := render.New(c.OutOrStdout())
	if len(fields) == 0 {
		res, typeSt, err := ops.GetAllFields(path, id, typeName)
		if err != nil {
			return err
		}
		return r.Record(id, render.BuildFields(typeSt, res.Fields))
	}
	res, err := ops.Get(path, id, typeName, fields)
	if err != nil {
		return err
	}
	rf, err := buildRenderFields(path, id, res.Fields, fields)
	if err != nil {
		return err
	}
	return r.Record(id, rf)
}

// runGetGroup emits the aggregate output for a group-prefix get.
// JSON mode emits {"records": [{id, fields}, ...]} matching the MCP
// scope-result shape. ANSI mode emits one notice line per child and a
// summary count so the terminal output is scannable.
func runGetGroup(c *cobra.Command, records []ops.ScopeRecord, id string, asJSON bool) error {
	if asJSON {
		out := make([]map[string]any, len(records))
		for i, rec := range records {
			out[i] = map[string]any{
				"id":     rec.ID,
				"fields": rec.Fields,
			}
		}
		enc := json.NewEncoder(c.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"records": out})
	}
	r := render.New(c.OutOrStdout())
	for _, rec := range records {
		if err := r.Notice(laslig.NoticeInfoLevel, "get", rec.ID, nil); err != nil {
			return err
		}
	}
	// Summary count line mirrors MCP scope output convention so CLI and
	// MCP group results look parallel.
	_, err := fmt.Fprintf(c.OutOrStdout(), "group %q: %d child record(s)\n", id, len(records))
	return err
}

// collectGetItems reconciles the positional shorthand and --batch forms
// into a single []getItem slice. Positional applies the top-level
// --fields to every id; --batch reads the heterogeneous JSON envelope
// and lets each item carry its own optional fields override.
func collectGetItems(stdin io.Reader, batch string, args, fields []string) ([]getItem, error) {
	if batch != "" {
		raw, err := readBatchEnvelope(stdin, batch)
		if err != nil {
			return nil, fmt.Errorf("ta get: %w", err)
		}
		items := make([]getItem, 0, len(raw))
		for i, entry := range raw {
			obj, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ta get: items[%d] must be an object", i)
			}
			id, _ := obj["id"].(string)
			if id == "" {
				return nil, fmt.Errorf("ta get: items[%d].id is required", i)
			}
			it := getItem{ID: id}
			if rawFields, ok := obj["fields"].([]any); ok {
				for j, fv := range rawFields {
					s, ok := fv.(string)
					if !ok {
						return nil, fmt.Errorf("ta get: items[%d].fields[%d] must be a string", i, j)
					}
					it.Fields = append(it.Fields, s)
				}
			}
			items = append(items, it)
		}
		return items, nil
	}
	items := make([]getItem, len(args))
	for i, id := range args {
		items[i] = getItem{ID: id, Fields: fields}
	}
	return items, nil
}

// validateGetItems guards the only batch-level get failure: an empty
// items array. Duplicate ids are intentionally allowed for reads —
// idempotent fetches stay cheap and the duplicate is preserved in
// input order in results[].
func validateGetItems(items []getItem) error {
	if len(items) == 0 {
		return errors.New("ta get: no items provided")
	}
	return nil
}

// runGetItems iterates items[] and runs each through ops.Get. A miss
// (record not found) is NOT a per-item error for reads — `found=false`
// surfaces in the result, matching MCP read semantics where missing
// records are a normal observable state, not an exceptional one.
func runGetItems(path string, items []getItem) []getItemResult {
	results := make([]getItemResult, len(items))
	for i, it := range items {
		res, err := ops.Get(path, it.ID, "", it.Fields)
		entry := getItemResult{ID: it.ID}
		if err != nil {
			// Records that don't exist surface as found=false rather
			// than per-item errors so batch reads stay observation-
			// friendly. Genuine failures (schema resolve, IO) keep the
			// error string for the caller.
			if isNotFound(err) {
				results[i] = entry
				continue
			}
			entry.Error = err.Error()
			results[i] = entry
			continue
		}
		entry.Found = true
		if len(it.Fields) > 0 {
			entry.Fields = res.Fields
		} else {
			entry.Bytes = string(res.Bytes)
		}
		results[i] = entry
	}
	return results
}

// emitGetBatch writes the get results envelope. JSON mode emits the
// {path, results} shape. Laslig mode emits one render block per result
// — found rows go through the standard renderer; misses surface as a
// single info notice line so the operator sees per-item state.
func emitGetBatch(w io.Writer, path string, results []getItemResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(getBatchResult{Path: path, Results: results})
	}
	r := render.New(w)
	for _, res := range results {
		switch {
		case res.Error != "":
			if _, err := fmt.Fprintf(w, "ta get: %s: %s\n", res.ID, res.Error); err != nil {
				return err
			}
		case !res.Found:
			if err := r.Notice(laslig.NoticeInfoLevel, "get", "not found: "+res.ID, nil); err != nil {
				return err
			}
		default:
			// Found case: a single info notice keeps batch laslig output
			// minimal. Fields-rendering for heterogeneous batch records
			// would need per-id schema dispatch the JSON envelope already
			// encodes far more cleanly; agents should pass --json for
			// structured batch reads.
			label := "found: " + res.ID
			if len(res.Fields) > 0 {
				label = "found (fields): " + res.ID
			}
			if err := r.Notice(laslig.NoticeInfoLevel, "get", label, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// anyGetErrored reports whether any read result carries a non-empty
// Error string. Misses (found=false with empty Error) do NOT count —
// they are an observable state, not a failure.
func anyGetErrored(results []getItemResult) bool {
	for _, r := range results {
		if r.Error != "" {
			return true
		}
	}
	return false
}

// runGetScope is the id-prefix branch of `ta get`. Walks every record
// in scope via ops.GetScope and emits either a sequence of laslig
// Section blocks (default) or a {"records": [...]} JSON envelope
// (--json). Matches the MCP `get` id-prefix response shape so CLI and
// MCP stay in lockstep.
func runGetScope(c *cobra.Command, path, id string, fields []string, limit int, all bool, asJSON bool) error {
	records, err := ops.GetScope(path, id, fields, limit, all)
	if err != nil {
		return err
	}
	if asJSON {
		return emitGetScopeJSON(c.OutOrStdout(), records)
	}
	r := render.New(c.OutOrStdout())
	if len(records) == 0 {
		return r.Notice(laslig.NoticeInfoLevel, "get", "no records in scope: "+id, nil)
	}
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return fmt.Errorf("resolve schema: %w", err)
	}
	for _, rec := range records {
		_, typeSt, err := lookupDBAndType(resolution.Registry, path, rec.ID)
		if err != nil {
			// Best-effort: render without typed fields.
			if err := r.Record(rec.ID, nil); err != nil {
				return err
			}
			continue
		}
		if err := r.Record(rec.ID, render.BuildFields(typeSt, rec.Fields)); err != nil {
			return err
		}
	}
	return nil
}

// emitGetScopeJSON writes the --json form of an id-prefix `ta get`.
// Shape mirrors the MCP tool's scopeResult: {"records": [{id, fields},
// ...]}. Always plural, even when len(records) == 1.
func emitGetScopeJSON(w io.Writer, records []ops.ScopeRecord) error {
	out := make([]map[string]any, len(records))
	for i, r := range records {
		out[i] = map[string]any{
			"id":     r.ID,
			"fields": r.Fields,
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"records": out})
}

// emitGetJSON writes the --json form of `get`. Two shapes: raw-bytes
// mode returns {"id": ..., "bytes": ...}; fields mode returns
// {"id": ..., "fields": {...}}.
func emitGetJSON(w io.Writer, id string, raw []byte, fields map[string]any, haveFields bool) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if haveFields {
		return enc.Encode(map[string]any{
			"id":     id,
			"fields": fields,
		})
	}
	return enc.Encode(map[string]any{
		"id":    id,
		"bytes": string(raw),
	})
}

// buildRenderFields pairs the MCP-decoded field values with their
// schema types so the renderer can dispatch string vs scalar vs
// structured rendering.
func buildRenderFields(path, id string, values map[string]any, names []string) ([]render.RenderField, error) {
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	dbDecl, typeSt, err := lookupDBAndType(resolution.Registry, path, id)
	if err != nil {
		return nil, err
	}
	_ = dbDecl
	out := make([]render.RenderField, 0, len(names))
	for _, name := range names {
		f, ok := typeSt.Fields[name]
		if !ok {
			return nil, fmt.Errorf("field %q not declared on %q", name, typeSt.Name)
		}
		out = append(out, render.RenderField{
			Name:  name,
			Type:  f.Type,
			Value: values[name],
		})
	}
	return out, nil
}
