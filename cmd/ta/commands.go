package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/schema"
)

// newGetCmd mirrors the MCP tool `get`. Without --fields the CLI
// synthesizes every declared field from the record and routes through
// the shared render.Renderer.Record helper — same visual shape as `ta
// search` hits, and the same dispatch `ta get --fields <list>` already
// uses (V2-PLAN §12.17.5 [B3]). With --fields the named field values
// are rendered per type. With --json the laslig path is bypassed;
// structured JSON is written for agent consumption (V2-PLAN §14.3).
//
// A scope-prefix `<section>` (e.g. `<db>`, `<db>.<type>`,
// `<db>.<instance>`, `<db>.<instance>.<type>`) returns every matching
// record in file-parse order; --limit (default 10, -n shorthand) and
// --all control the cap. Single-record addresses (fully qualified)
// silently ignore --limit / --all (V2-PLAN §12.17.5 [B2]).
func newGetCmd() *cobra.Command {
	var fields []string
	var asJSON bool
	var limit int
	var all bool
	var typeName string
	cmd := &cobra.Command{
		Use:   "get <section>",
		Short: "Read one record or every record under a scope prefix; optionally extract declared field values",
		Long: "Mirrors the MCP tool `get`. A fully-qualified address " +
			"('<db>.<type>.<id-path>' or '<db>.<instance>.<type>.<id-path>') " +
			"returns one record; without --fields every declared field is " +
			"rendered through the shared per-field helper (string fields as " +
			"markdown, scalars as label:value, arrays/tables as fenced JSON " +
			"— V2-PLAN §12.17.5 [B3]); with --fields name[,name...] the " +
			"named subset is rendered. A scope-prefix address ('<db>', " +
			"'<db>.<type>', '<db>.<instance>', '<db>.<instance>.<type>') " +
			"returns every matching record in file-parse order as a " +
			"sequence of laslig Section blocks, or --json " +
			"{\"records\":[{section, fields}, ...]}. --limit (default 10, " +
			"-n shorthand) and --all control the cap for scope-prefix " +
			"addresses; both are silently ignored for fully-qualified " +
			"single-record addresses and are mutually exclusive (V2-PLAN " +
			"§12.17.5 [B2]). With --json the laslig path is bypassed and " +
			"JSON is written for agent consumption. --path defaults to " +
			"cwd; relative or absolute accepted (V2-PLAN §12.17.5 [A1]).",
		Example: "  ta get plans.task.task-001\n" +
			"  ta get --path /abs/proj plans.task.task-001 --fields status,body\n" +
			"  ta get plans.task.task-001 --json\n" +
			"  ta get plans.task --all --json\n" +
			"  ta get plan_db.drop_a --limit 5",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			section := args[0]
			isScope, err := ops.IsScopeAddress(path, section)
			if err != nil {
				return err
			}
			if isScope {
				return runGetScope(c, path, section, fields, limit, all, asJSON)
			}
			if asJSON {
				res, err := ops.Get(path, section, typeName, fields)
				if err != nil {
					return err
				}
				return emitGetJSON(c.OutOrStdout(), section, res.Bytes, res.Fields, len(fields) > 0)
			}
			r := render.New(c.OutOrStdout())
			if len(fields) == 0 {
				res, typeSt, err := ops.GetAllFields(path, section, typeName)
				if err != nil {
					return err
				}
				return r.Record(section, render.BuildFields(typeSt, res.Fields))
			}
			res, err := ops.Get(path, section, typeName, fields)
			if err != nil {
				return err
			}
			rf, err := buildRenderFields(path, section, res.Fields, fields)
			if err != nil {
				return err
			}
			return r.Record(section, rf)
		},
	}
	cmd.Flags().StringSliceVar(&fields, "fields", nil, "comma-separated declared field names to extract")
	cmd.Flags().StringSliceVar(&fields, "field", nil, "declared field name to extract (repeatable)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "cap the record count at N when <section> is a scope prefix (default 10; ignored for single-record addresses; mutually exclusive with --all)")
	cmd.Flags().BoolVar(&all, "all", false, "return every record when <section> is a scope prefix (ignored for single-record addresses; mutually exclusive with --limit)")
	cmd.Flags().StringVar(&typeName, "type", "", "optional declared type name; cross-checked against the address (PLAN §12.17.9 Phase 9.4)")
	cmd.MarkFlagsMutuallyExclusive("limit", "all")
	addPathFlag(cmd)
	return cmd
}

// runGetScope is the scope-prefix branch of `ta get`. Walks every
// record in scope via ops.GetScope and emits either a sequence of
// laslig Section blocks (default) or a {"records": [...]} JSON
// envelope (--json). Matches the MCP `get` scope-prefix response
// shape so CLI and MCP stay in lockstep (§12.17.5 [B2]).
func runGetScope(c *cobra.Command, path, section string, fields []string, limit int, all bool, asJSON bool) error {
	records, err := ops.GetScope(path, section, fields, limit, all)
	if err != nil {
		return err
	}
	if asJSON {
		return emitGetScopeJSON(c.OutOrStdout(), records)
	}
	r := render.New(c.OutOrStdout())
	if len(records) == 0 {
		return r.Notice(laslig.NoticeInfoLevel, "get", "no records in scope: "+section, nil)
	}
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return fmt.Errorf("resolve schema: %w", err)
	}
	for _, rec := range records {
		_, typeSt, err := lookupDBAndType(resolution.Registry, path, rec.Section)
		if err != nil {
			// Best-effort: render without typed fields.
			if err := r.Record(rec.Section, nil); err != nil {
				return err
			}
			continue
		}
		if err := r.Record(rec.Section, render.BuildFields(typeSt, rec.Fields)); err != nil {
			return err
		}
	}
	return nil
}

// emitGetScopeJSON writes the --json form of a scope-prefix `ta get`.
// Shape mirrors the MCP tool's scopeResult: {"records": [{section,
// fields}, ...]}. Always plural, even when len(records) == 1.
func emitGetScopeJSON(w io.Writer, records []ops.ScopeRecord) error {
	out := make([]map[string]any, len(records))
	for i, r := range records {
		out[i] = map[string]any{
			"section": r.Section,
			"fields":  r.Fields,
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"records": out})
}

// emitGetJSON writes the --json form of `get`. Two shapes: raw-bytes
// mode returns {"section": ..., "bytes": ...}; fields mode returns
// {"section": ..., "fields": {...}}.
func emitGetJSON(w io.Writer, section string, raw []byte, fields map[string]any, haveFields bool) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if haveFields {
		return enc.Encode(map[string]any{
			"section": section,
			"fields":  fields,
		})
	}
	return enc.Encode(map[string]any{
		"section": section,
		"bytes":   string(raw),
	})
}

// renderVerboseRecord fetches the named record and renders its bytes
// via renderRawRecord. Used by the --verbose flag on create / update
// to echo the post-mutation record content after the success notice
// per V2-PLAN §13.1. Returns any fetch error so the caller can surface
// it rather than silently skip the echo.
func renderVerboseRecord(w io.Writer, path, section string) error {
	res, err := ops.Get(path, section, "", nil)
	if err != nil {
		return fmt.Errorf("verbose echo: %w", err)
	}
	return renderRawRecord(render.New(w), path, section, res.Bytes)
}

// renderRawRecord routes an unparsed record through glamour. TOML bytes
// are wrapped in a ```toml fence so code highlighting survives; MD bytes
// are passed through unchanged because they're already markdown.
func renderRawRecord(r *render.Renderer, path, section string, raw []byte) error {
	format, err := dbFormatFor(path, section)
	if err != nil {
		// Fall back to raw pass-through rather than failing the whole
		// render — we already have the bytes, no reason to hide them.
		return r.Markdown(string(raw))
	}
	body := string(raw)
	if format == schema.FormatTOML {
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body = "```toml\n" + body + "```\n"
	}
	return r.Markdown(body)
}

// dbFormatFor looks up the db format for the address's first segment.
// Used to pick a render branch (TOML fenced vs MD pass-through).
func dbFormatFor(path, section string) (schema.Format, error) {
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return "", err
	}
	resolver := db.NewResolver(path, resolution.Registry)
	_, dbDecl, err := resolver.ParseAddress(section)
	if err != nil {
		return "", fmt.Errorf("address %q: %w", section, err)
	}
	return dbDecl.Format, nil
}

// buildRenderFields pairs the MCP-decoded field values with their
// schema types so the renderer can dispatch string vs scalar vs
// structured rendering.
func buildRenderFields(path, section string, values map[string]any, names []string) ([]render.RenderField, error) {
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	// Resolve type for the address. The Phase 9.2 grammar
	// `<file-relpath>.<type>.<id>` is parsed by the resolver; we
	// need the type descriptor to drive structured rendering.
	dbDecl, typeSt, err := lookupDBAndType(resolution.Registry, path, section)
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

func lookupDBAndType(reg schema.Registry, projectPath, section string) (schema.DB, schema.SectionType, error) {
	resolver := db.NewResolver(projectPath, reg)
	addr, dbDecl, err := resolver.ParseAddress(section)
	if err != nil {
		return schema.DB{}, schema.SectionType{}, fmt.Errorf("address %q: %w", section, err)
	}
	t, ok := dbDecl.Types[addr.Type]
	if !ok {
		return dbDecl, schema.SectionType{}, fmt.Errorf("type %q not declared on db %q", addr.Type, dbDecl.Name)
	}
	return dbDecl, t, nil
}

// newListSectionsCmd mirrors the MCP tool `list_sections` (V2-PLAN §3.2
// and §12.17.5 [A2]). The CLI takes a project directory via `--path`
// (default cwd) plus an optional scope (either `--scope <value>` or a
// second positional — not both). Output emits full project-level
// dotted addresses so copy-paste composes with `get` / `update` /
// `delete` addresses. `--limit <N>` (default 10, `-n` shorthand) and
// `--all` control the cap; they are mutually exclusive.
func newListSectionsCmd() *cobra.Command {
	var asJSON bool
	var scope string
	var limit int
	var all bool
	cmd := &cobra.Command{
		Use:   "list-sections [scope]",
		Short: "Enumerate record addresses under a scope; mirrors MCP tool `list_sections`.",
		Long: "Mirrors the MCP tool `list_sections` (V2-PLAN §3.2). Walks every " +
			"record in scope and emits its full project-level dotted " +
			"address (`<db>.<type>.<id-path>` for single-instance dbs, " +
			"`<db>.<instance>.<type>.<id-path>` for multi-instance). Scope " +
			"may be supplied via --scope or as the optional positional; " +
			"omitted = whole project. --limit caps the list (default 10, " +
			"-n shorthand); --all returns every match. --path defaults to " +
			"cwd; relative or absolute accepted (V2-PLAN §12.17.5 [A1]).",
		Example: `  ta list-sections
  ta list-sections plan_db
  ta list-sections --scope plan_db.ta
  ta list-sections --scope plan_db --all --json`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
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
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().StringVar(&scope, "scope", "", "<db> | <db>.<type> | <db>.<instance> | <db>.<type>.<id-prefix> | <db>.<instance>.<type>(.<id-prefix>)?")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "cap the list at N addresses (default 10)")
	cmd.Flags().BoolVar(&all, "all", false, "return every match (disables --limit)")
	cmd.MarkFlagsMutuallyExclusive("limit", "all")
	addPathFlag(cmd)
	return cmd
}

// resolveListScope reconciles the `--scope` flag with the optional
// positional scope argument. Per V2-PLAN §12.17.5 [A2] the positional
// is a convenience for --scope; supplying both forms at once is
// ambiguous and errors. Empty scope (neither form set) means "whole
// project" and is returned as "".
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

func newCreateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	var typeName string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "create <section>",
		Short: "Create a new record (fails if it exists); mirrors MCP tool `create`.",
		Long: "Create a new record at the given address. Fails if the record " +
			"already exists (V2-PLAN §3.4). Creates the backing file and any " +
			"intermediate directories on first use. --type names the declared " +
			"record type; PLAN §12.17.9 Phase 9.4 makes it the orthogonal " +
			"authoritative source. With --verbose, the newly-created record " +
			"content is echoed after the success notice per V2-PLAN §13.1. " +
			"--path defaults to cwd; relative or absolute accepted (V2-PLAN " +
			"§12.17.5 [A1]).",
		Example: "  ta create plans.task.task-001 --type task --data '{\"id\":\"TASK-001\",\"status\":\"todo\"}'\n" +
			"  ta create --path /abs/proj plans.task.task-001 --type task --data-file payload.json\n" +
			"  cat payload.json | ta create plans.task.task-001 --type task --data-file -",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			section := args[0]
			data, err := collectCreateData(c, path, section, dataInline, dataFile)
			if err != nil {
				return err
			}
			targetPath, sources, err := runCreate(path, section, typeName, data)
			if err != nil {
				return err
			}
			if err := noticeMutation(c.OutOrStdout(), "created", section, targetPath, sources); err != nil {
				return err
			}
			if verbose {
				return renderVerboseRecord(c.OutOrStdout(), path, section)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin")
	cmd.Flags().StringVar(&typeName, "type", "", "declared record type name (REQUIRED; PLAN §12.17.9 Phase 9.4)")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the newly-created record after the success notice")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	if err := cmd.MarkFlagRequired("type"); err != nil {
		// MarkFlagRequired only errors when the named flag is not registered.
		// We just registered it above, so this is a programming-error guard
		// rather than a runtime path; surface via panic per cobra norms.
		panic(fmt.Sprintf("ta: mark --type required: %v", err))
	}
	addPathFlag(cmd)
	return cmd
}

func newUpdateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	var typeName string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "update <section>",
		Short: "PATCH an existing record; mirrors MCP tool `update`.",
		Long: "PATCH-style update: --data is a partial overlay, not a full " +
			"replacement. Provided fields overwrite their stored values; " +
			"unspecified fields keep their bytes verbatim. Empty --data ({}) " +
			"is a no-op success. Null on a non-required field clears it; " +
			"null on a required field with a schema default resets it to " +
			"that default; null on a required field with no default errors. " +
			"The merged record is atomically re-validated (V2-PLAN §3.5 / " +
			"§12.17.5 [B1]). Fails if the backing file does not exist; " +
			"creates the record within the file when absent (record-level " +
			"upsert). With --verbose, the updated record is echoed after " +
			"the success notice per V2-PLAN §13.1. --path defaults to cwd; " +
			"relative or absolute accepted (V2-PLAN §12.17.5 [A1]).",
		Example: "  ta update plans.task.task-001 --data '{\"status\":\"done\"}'\n" +
			"  ta update plans.task.task-001 --data '{\"notes\":null}'    # clear optional field\n" +
			"  ta update --path /abs/proj plans.task.task-001 --data-file patch.json --verbose",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			section := args[0]
			data, err := collectUpdateData(c, path, section, dataInline, dataFile)
			if err != nil {
				return err
			}
			targetPath, sources, err := runUpdate(path, section, typeName, data)
			if err != nil {
				return err
			}
			if err := noticeMutation(c.OutOrStdout(), "updated", section, targetPath, sources); err != nil {
				return err
			}
			if verbose {
				return renderVerboseRecord(c.OutOrStdout(), path, section)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin")
	cmd.Flags().StringVar(&typeName, "type", "", "optional declared type name; cross-checked against the address (PLAN §12.17.9 Phase 9.4)")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the updated record after the success notice")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	addPathFlag(cmd)
	return cmd
}

func newDeleteCmd() *cobra.Command {
	var typeName string
	cmd := &cobra.Command{
		Use:   "delete <section>",
		Short: "Remove a record, file, or instance directory; mirrors MCP tool `delete`.",
		Long: "Remove a record (bytes spliced out), a single-instance data " +
			"file, or a multi-instance instance dir/file. Whole multi-instance " +
			"db deletes error as ambiguous; zero the instances first or route " +
			"through `schema delete --kind db` (V2-PLAN §3.6). --type is " +
			"optional and cross-checks the supplied type against the address " +
			"(PLAN §12.17.9 Phase 9.4). --path defaults to cwd; relative or " +
			"absolute accepted (V2-PLAN §12.17.5 [A1]).",
		Example: `  ta delete plans.task.task-001
  ta delete --path /abs/proj plans
  ta delete plan_db.drop-3`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			section := args[0]
			targetPath, sources, err := runDelete(path, section, typeName)
			if err != nil {
				return err
			}
			return noticeMutation(c.OutOrStdout(), "deleted", section, targetPath, sources)
		},
	}
	cmd.Flags().StringVar(&typeName, "type", "", "optional declared type name; cross-checked against the address (PLAN §12.17.9 Phase 9.4)")
	addPathFlag(cmd)
	return cmd
}

func newSchemaCmd() *cobra.Command {
	var action string
	var kind string
	var name string
	var dataInline string
	var dataFile string
	var verbose bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "schema [section]",
		Short: "Inspect or mutate the resolved schema; mirrors MCP tool `schema`.",
		Long: "With action=get (default), renders the resolved schema; an " +
			"optional section/scope narrows to one db or type. Passing the " +
			"reserved value `ta_schema` prints the embedded meta-schema " +
			"literal. With action=create|update|delete, mutates the project " +
			"`.ta/schema.toml` (re-validated on every mutation with atomic " +
			"rollback — V2-PLAN §4.6). With --json the laslig path is " +
			"bypassed and JSON is written for agent consumption (action=get " +
			"only; mutations always print the success notice). --path defaults " +
			"to cwd; relative or absolute accepted (V2-PLAN §12.17.5 [A1]).",
		Example: `  ta schema
  ta schema plans.task --json
  ta schema ta_schema
  ta schema --path /abs/proj --action=create --kind=type --name=plans.note --data '{...}'`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			var scope string
			if len(args) == 1 {
				scope = args[0]
			}
			if action == "" || action == "get" {
				if asJSON {
					return runSchemaGetJSON(c.OutOrStdout(), path, scope)
				}
				return runSchemaGet(c.OutOrStdout(), path, scope)
			}
			raw, err := readJSONDataOptional(dataInline, dataFile, c.InOrStdin(), action == "delete")
			if err != nil {
				return err
			}
			var data map[string]any
			if raw != nil {
				if err := json.Unmarshal(raw, &data); err != nil {
					return fmt.Errorf("parse data JSON: %w", err)
				}
			}
			sources, err := runSchemaMutate(path, action, kind, name, data)
			if err != nil {
				return err
			}
			if err := noticeMutation(c.OutOrStdout(), "schema "+action, name, "", sources); err != nil {
				return err
			}
			if verbose {
				return runSchemaGet(c.OutOrStdout(), path, "")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&action, "action", "get", "one of get | create | update | delete")
	cmd.Flags().StringVar(&kind, "kind", "", "db | type | field (for action != get)")
	cmd.Flags().StringVar(&name, "name", "", "dotted schema address (for action != get)")
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON payload (for action create|update)")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON payload from file; use `-` for stdin")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the post-mutation schema after the success notice (no effect on action=get)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output (action=get)")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	addPathFlag(cmd)
	return cmd
}

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
			"--path defaults to cwd; relative or absolute accepted " +
			"(V2-PLAN §12.17.5 [A1] / [A2.2]).",
		Example: "  ta search --scope=plans.task --match '{\"status\":\"todo\"}'\n" +
			"  ta search --path /abs/proj --scope=plans.task --query='TODO' --field=body\n" +
			"  ta search --scope=plans.task --all --json",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
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
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "<db> | <db>.<type> | <db>.<instance> | <db>.<type>.<id-prefix>")
	cmd.Flags().StringVar(&matchJSON, "match", "", "JSON object of {field: exact-value}")
	cmd.Flags().StringVar(&query, "query", "", "Go RE2 regex matched against string fields")
	cmd.Flags().StringVar(&field, "field", "", "restrict --query to one string field")
	cmd.Flags().StringVar(&typeName, "type", "", "optional declared type name; post-walk filter on hit addresses (PLAN §12.17.9 Phase 9.4)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "cap the hit count at N (default 10)")
	cmd.Flags().BoolVar(&all, "all", false, "return every match (disables --limit)")
	cmd.MarkFlagsMutuallyExclusive("limit", "all")
	addPathFlag(cmd)
	return cmd
}

// emitSearchJSON writes the --json form of `search`. Shape:
// {"hits": [{"section": "...", "bytes": "...", "fields": {...}}]}.
func emitSearchJSON(w io.Writer, hits []ops.SearchHit) error {
	out := make([]map[string]any, len(hits))
	for i, h := range hits {
		out[i] = map[string]any{
			"section": h.Section,
			"bytes":   string(h.Bytes),
			"fields":  h.Fields,
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
		_, typeSt, err := lookupDBAndType(resolution.Registry, path, hit.Section)
		if err != nil {
			// Best-effort: render without typed fields.
			if err := r.Record(hit.Section, nil); err != nil {
				return err
			}
			continue
		}
		if err := r.Record(hit.Section, render.BuildFields(typeSt, hit.Fields)); err != nil {
			return err
		}
	}
	return nil
}

// ---- helpers (CLI-local mirrors of the MCP handlers) -----------------

// collectCreateData is the create-side entrypoint for field data.
// Preserves the non-interactive --data / --data-file contract and, when
// neither is set and stdin is a TTY, runs the interactive huh form
// built from the resolved type's declared fields (V2-PLAN §12.17.5
// [D1]). Off-TTY with no flags errors politely so agents and scripts
// fail loudly instead of hanging on stdin.
func collectCreateData(c *cobra.Command, path, section, dataInline, dataFile string) (map[string]any, error) {
	if dataInline != "" || dataFile != "" {
		raw, err := readJSONData(dataInline, dataFile, c.InOrStdin())
		if err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("parse data JSON: %w", err)
		}
		return data, nil
	}
	if !ttyInteractive(false) {
		return nil, errors.New("input required — pass --data '{...}' or --data-file <path>, or run interactively in a TTY")
	}
	typeSt, err := resolveTypeForSection(path, section)
	if err != nil {
		return nil, err
	}
	form, _, collect := FormFor(typeSt, nil, false)
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("form: %w", err)
	}
	return collect()
}

// collectUpdateData is the update-side entrypoint. Same shape as
// collectCreateData, but when no --data / --data-file is passed and
// stdin is a TTY, the form prefills existing values from the stored
// record so the user edits in place. Blank submissions retain per PATCH
// semantics (V2-PLAN §3.5).
func collectUpdateData(c *cobra.Command, path, section, dataInline, dataFile string) (map[string]any, error) {
	if dataInline != "" || dataFile != "" {
		raw, err := readJSONData(dataInline, dataFile, c.InOrStdin())
		if err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("parse data JSON: %w", err)
		}
		return data, nil
	}
	if !ttyInteractive(false) {
		return nil, errors.New("input required — pass --data '{...}' or --data-file <path>, or run interactively in a TTY")
	}
	res, typeSt, err := ops.GetAllFields(path, section, "")
	if err != nil {
		return nil, err
	}
	form, _, collect := FormFor(typeSt, res.Fields, true)
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("form: %w", err)
	}
	return collect()
}

// resolveTypeForSection returns the SectionType that the address names,
// resolving the db + type from the project registry. Used by the
// create path, which cannot rely on an existing record for schema
// lookup.
func resolveTypeForSection(path, section string) (schema.SectionType, error) {
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return schema.SectionType{}, fmt.Errorf("resolve schema: %w", err)
	}
	_, typeSt, err := lookupDBAndType(resolution.Registry, path, section)
	if err != nil {
		return schema.SectionType{}, err
	}
	return typeSt, nil
}

func readJSONData(inline, file string, stdin io.Reader) ([]byte, error) {
	if inline != "" {
		return []byte(inline), nil
	}
	switch file {
	case "":
		return nil, errors.New("must provide --data <json> or --data-file <path>")
	case "-":
		return io.ReadAll(stdin)
	default:
		return os.ReadFile(file)
	}
}

// readJSONDataOptional is a variant for tools that accept no data (e.g.
// schema delete). Returns (nil, nil) when optional=true and no flag is
// set.
func readJSONDataOptional(inline, file string, stdin io.Reader, optional bool) ([]byte, error) {
	if inline == "" && file == "" {
		if optional {
			return nil, nil
		}
		return nil, errors.New("must provide --data <json> or --data-file <path>")
	}
	return readJSONData(inline, file, stdin)
}

func runSchemaGet(w io.Writer, path, scope string) error {
	if scope == schema.MetaSchemaPath {
		return renderMetaSchema(w)
	}
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	dbs := resolution.Registry.DBs
	if scope != "" {
		if t, ok := resolution.Registry.Lookup(scope); ok {
			dbDecl, _ := resolution.Registry.LookupDB(scope)
			dbDecl.Types = map[string]schema.SectionType{t.Name: t}
			dbs = map[string]schema.DB{dbDecl.Name: dbDecl}
		} else if !strings.Contains(scope, ".") {
			if dbDecl, ok := resolution.Registry.LookupDB(scope); ok {
				dbs = map[string]schema.DB{dbDecl.Name: dbDecl}
			} else {
				return fmt.Errorf("no schema registered for section %q in %s", scope, path)
			}
		} else {
			return fmt.Errorf("no schema registered for section %q in %s", scope, path)
		}
	}
	return render.New(w).SchemaFlow(path, scope, resolution.Sources, dbs)
}

// runSchemaGetJSON mirrors runSchemaGet but writes JSON for agent
// consumption. Shape mirrors the MCP `schema` tool's get response: a
// map keyed by db name, each db carrying its types and fields. The
// `ta_schema` scope short-circuits to the embedded meta-schema literal
// for parity with the laslig path.
func runSchemaGetJSON(w io.Writer, path, scope string) error {
	if scope == schema.MetaSchemaPath {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"scope":            scope,
			"meta_schema_toml": schema.MetaSchemaTOML,
		})
	}
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	dbs := resolution.Registry.DBs
	if scope != "" {
		if t, ok := resolution.Registry.Lookup(scope); ok {
			dbDecl, _ := resolution.Registry.LookupDB(scope)
			dbDecl.Types = map[string]schema.SectionType{t.Name: t}
			dbs = map[string]schema.DB{dbDecl.Name: dbDecl}
		} else if !strings.Contains(scope, ".") {
			if dbDecl, ok := resolution.Registry.LookupDB(scope); ok {
				dbs = map[string]schema.DB{dbDecl.Name: dbDecl}
			} else {
				return fmt.Errorf("no schema registered for section %q in %s", scope, path)
			}
		} else {
			return fmt.Errorf("no schema registered for section %q in %s", scope, path)
		}
	}
	payload := map[string]any{
		"schema_paths": resolution.Sources,
		"dbs":          schemaDBsToJSON(dbs),
	}
	if scope != "" {
		payload["scope"] = scope
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// schemaDBsToJSON converts the registry DB map to a plain JSON-friendly
// shape. Mirrors internal/mcpsrv/tools.go:toDBsView but lives here to
// keep the CLI self-sufficient — §13.3 firewall says mcpsrv must not
// import render, and the symmetric rule ("render must not import mcpsrv
// internals") applies by analogy.
func schemaDBsToJSON(dbs map[string]schema.DB) map[string]any {
	out := make(map[string]any, len(dbs))
	for name, db := range dbs {
		out[name] = map[string]any{
			"name":        db.Name,
			"description": db.Description,
			"paths":       db.Paths,
			"format":      string(db.Format),
			"types":       schemaTypesToJSON(db.Types),
		}
	}
	return out
}

func schemaTypesToJSON(types map[string]schema.SectionType) map[string]any {
	out := make(map[string]any, len(types))
	for name, t := range types {
		fields := make(map[string]any, len(t.Fields))
		for fn, f := range t.Fields {
			fe := map[string]any{
				"type":     string(f.Type),
				"required": f.Required,
			}
			if f.Description != "" {
				fe["description"] = f.Description
			}
			if len(f.Enum) > 0 {
				fe["enum"] = f.Enum
			}
			if f.Format != "" {
				fe["format"] = f.Format
			}
			if f.Default != nil {
				fe["default"] = f.Default
			}
			fields[fn] = fe
		}
		entry := map[string]any{
			"name":   t.Name,
			"fields": fields,
		}
		if t.Description != "" {
			entry["description"] = t.Description
		}
		if t.Heading != 0 {
			entry["heading"] = t.Heading
		}
		out[name] = entry
	}
	return out
}

// renderMetaSchema prints the embedded meta-schema TOML literal directly —
// glamour-rendering a raw TOML body would add no value and hurt
// copy-paste. This is the CLI counterpart to MCP's `schema(scope=
// "ta_schema")`.
func renderMetaSchema(w io.Writer) error {
	body := "# ta_schema — embedded meta-schema\n\n```toml\n" + schema.MetaSchemaTOML + "```\n"
	return render.New(w).Markdown(body)
}

func noticeMutation(w io.Writer, action, section, filePath string, sources []string) error {
	body := section
	if filePath != "" {
		body = section + "\n" + filePath
	}
	return render.New(w).Success(action, body, sources)
}

// runCreate / runUpdate / runDelete / runSchemaMutate are thin
// wrappers over the shared ops.* endpoints. Keeping them here means the
// CLI's error surface is pure-Go (no MCP envelope) while the MCP
// handlers in internal/mcpsrv/tools.go reuse exactly the same paths.

func runCreate(path, section, typeName string, data map[string]any) (string, []string, error) {
	return ops.Create(path, section, typeName, data)
}

func runUpdate(path, section, typeName string, data map[string]any) (string, []string, error) {
	return ops.Update(path, section, typeName, data)
}

func runDelete(path, section, typeName string) (string, []string, error) {
	return ops.Delete(path, section, typeName)
}

func runSchemaMutate(path, action, kind, name string, data map[string]any) ([]string, error) {
	return ops.MutateSchema(path, action, kind, name, data)
}
