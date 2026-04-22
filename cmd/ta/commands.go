package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/mcpsrv"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/schema"
)

// newGetCmd mirrors the MCP tool `get`. Without --fields the CLI
// renders the raw on-disk bytes: TOML records go through a ```toml
// fenced markdown block so glamour highlights them; MD records are
// passed directly through the markdown renderer. With --fields the
// field values are dispatched through render.Renderer.Record so string
// fields pick up markdown rendering per V2-PLAN §13.2.
func newGetCmd() *cobra.Command {
	var fields []string
	cmd := &cobra.Command{
		Use:   "get <path> <section>",
		Short: "Read one record; optionally extract declared field values",
		Long: "Mirrors the MCP tool `get`. Without --fields the raw record " +
			"bytes are rendered through laslig (TOML wrapped in a ```toml " +
			"code fence; markdown passed through glamour). With --fields " +
			"name[,name...] or repeated --field <name> the named field values " +
			"are rendered per type: string fields as markdown, scalars as " +
			"label:value, arrays/tables as fenced JSON.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, section := args[0], args[1]
			res, err := mcpsrv.Get(path, section, fields)
			if err != nil {
				return err
			}
			r := render.New(c.OutOrStdout())
			if len(fields) == 0 {
				return renderRawRecord(r, path, section, res.Bytes)
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
	return cmd
}

// renderVerboseRecord fetches the named record and renders its bytes
// via renderRawRecord. Used by the --verbose flag on create / update
// to echo the post-mutation record content after the success notice
// per V2-PLAN §13.1. Returns any fetch error so the caller can surface
// it rather than silently skip the echo.
func renderVerboseRecord(w io.Writer, path, section string) error {
	res, err := mcpsrv.Get(path, section, nil)
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
	resolution, err := mcpsrv.ResolveProject(path)
	if err != nil {
		return "", err
	}
	firstDot := strings.IndexByte(section, '.')
	dbName := section
	if firstDot >= 0 {
		dbName = section[:firstDot]
	}
	dbDecl, ok := resolution.Registry.DBs[dbName]
	if !ok {
		return "", fmt.Errorf("db %q not declared", dbName)
	}
	return dbDecl.Format, nil
}

// buildRenderFields pairs the MCP-decoded field values with their
// schema types so the renderer can dispatch string vs scalar vs
// structured rendering.
func buildRenderFields(path, section string, values map[string]any, names []string) ([]render.RenderField, error) {
	resolution, err := mcpsrv.ResolveProject(path)
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	// Resolve type for the address. We need <db>.<type> — for multi-
	// instance addresses strip the <instance> segment; mirrors
	// mcpsrv.validationPath.
	dbDecl, typeSt, err := lookupDBAndType(resolution.Registry, section)
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

func lookupDBAndType(reg schema.Registry, section string) (schema.DB, schema.SectionType, error) {
	parts := strings.Split(section, ".")
	if len(parts) < 2 {
		return schema.DB{}, schema.SectionType{}, fmt.Errorf("address %q: too few segments", section)
	}
	dbDecl, ok := reg.DBs[parts[0]]
	if !ok {
		return schema.DB{}, schema.SectionType{}, fmt.Errorf("db %q not declared", parts[0])
	}
	var typeName string
	switch dbDecl.Shape {
	case schema.ShapeFile:
		typeName = parts[1]
	default:
		if len(parts) < 3 {
			return schema.DB{}, schema.SectionType{}, fmt.Errorf("address %q: multi-instance needs <db>.<instance>.<type>...", section)
		}
		typeName = parts[2]
	}
	t, ok := dbDecl.Types[typeName]
	if !ok {
		return dbDecl, schema.SectionType{}, fmt.Errorf("type %q not declared on db %q", typeName, dbDecl.Name)
	}
	return dbDecl, t, nil
}

func newListSectionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-sections <path>",
		Short: "Enumerate every section in a TOML file, in file order",
		Long: "Mirrors the MCP tool `list_sections`. When the target file does " +
			"not exist yet, the list is empty rather than erroring — matches " +
			"the MCP behavior callers already depend on.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path := args[0]
			f, err := toml.Parse(path)
			if err != nil && !errors.Is(err, toml.ErrNotExist) {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			var paths []string
			if f != nil {
				paths = f.Paths()
			}
			return render.New(c.OutOrStdout()).List(path, paths, "(no sections)")
		},
	}
}

func newCreateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	var pathHint string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "create <path> <section>",
		Short: "Create a new record (fails if it exists); mirrors MCP tool `create`.",
		Long: "Create a new record at the given address. Fails if the record " +
			"already exists (V2-PLAN §3.4). Creates the backing file and any " +
			"intermediate directories on first use. For file-per-instance dbs, " +
			"--path-hint disambiguates flat vs nested placement. With --verbose, " +
			"the newly-created record content is echoed after the success " +
			"notice per V2-PLAN §13.1.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, section := args[0], args[1]
			raw, err := readJSONData(dataInline, dataFile, c.InOrStdin())
			if err != nil {
				return err
			}
			var data map[string]any
			if err := json.Unmarshal(raw, &data); err != nil {
				return fmt.Errorf("parse data JSON: %w", err)
			}
			targetPath, sources, err := runCreate(path, section, pathHint, data)
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
	cmd.Flags().StringVar(&pathHint, "path-hint", "", "relative placement hint inside a collection db's root")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the newly-created record after the success notice")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	return cmd
}

func newUpdateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "update <path> <section>",
		Short: "Update an existing record; mirrors MCP tool `update`.",
		Long: "Update an existing record. Fails if the backing file does not " +
			"exist (V2-PLAN §3.5). Creates the record within the file if the " +
			"file exists but the record does not (record-level upsert). With " +
			"--verbose, the updated record content is echoed after the success " +
			"notice per V2-PLAN §13.1.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, section := args[0], args[1]
			raw, err := readJSONData(dataInline, dataFile, c.InOrStdin())
			if err != nil {
				return err
			}
			var data map[string]any
			if err := json.Unmarshal(raw, &data); err != nil {
				return fmt.Errorf("parse data JSON: %w", err)
			}
			targetPath, sources, err := runUpdate(path, section, data)
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
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the updated record after the success notice")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	return cmd
}

func newDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <path> <section>",
		Short: "Remove a record, file, or instance directory; mirrors MCP tool `delete`.",
		Long: "Remove a record (bytes spliced out), a single-instance data " +
			"file, or a multi-instance instance dir/file. Whole multi-instance " +
			"db deletes error as ambiguous; zero the instances first or route " +
			"through `schema delete --kind db` (V2-PLAN §3.6).",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, section := args[0], args[1]
			targetPath, sources, err := runDelete(path, section)
			if err != nil {
				return err
			}
			return noticeMutation(c.OutOrStdout(), "deleted", section, targetPath, sources)
		},
	}
}

func newSchemaCmd() *cobra.Command {
	var action string
	var kind string
	var name string
	var dataInline string
	var dataFile string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "schema <path> [section]",
		Short: "Inspect or mutate the resolved schema; mirrors MCP tool `schema`.",
		Long: "With action=get (default), renders the resolved schema; an " +
			"optional section/scope narrows to one db or type. Passing the " +
			"reserved value `ta_schema` prints the embedded meta-schema " +
			"literal. With action=create|update|delete, mutates the project " +
			"`.ta/schema.toml` (re-validated on every mutation with atomic " +
			"rollback — V2-PLAN §4.6).",
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path := args[0]
			var scope string
			if len(args) == 2 {
				scope = args[1]
			}
			if action == "" || action == "get" {
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
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	return cmd
}

// newSearchCmd mirrors the MCP tool `search` (V2-PLAN §3.7 / §7). The
// CLI renders hits as one laslig card per record with the string fields
// glamour-rendered per §13.1 / §13.2.
func newSearchCmd() *cobra.Command {
	var scope string
	var matchJSON string
	var query string
	var field string
	cmd := &cobra.Command{
		Use:   "search <path>",
		Short: "Structured + regex search across records; mirrors MCP tool `search`.",
		Long: "Walks declared records under --scope, applies --match exact-match " +
			"filters on typed scalar fields (JSON object), then optionally " +
			"applies --query regex against string fields (restricted to " +
			"--field when set). One laslig card per hit.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path := args[0]
			var match map[string]any
			if matchJSON != "" {
				if err := json.Unmarshal([]byte(matchJSON), &match); err != nil {
					return fmt.Errorf("parse --match JSON: %w", err)
				}
			}
			hits, err := mcpsrv.Search(path, scope, match, query, field)
			if err != nil {
				return err
			}
			return renderSearchHits(c.OutOrStdout(), path, hits)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "<db> | <db>.<type> | <db>.<instance> | <db>.<type>.<id-prefix>")
	cmd.Flags().StringVar(&matchJSON, "match", "", "JSON object of {field: exact-value}")
	cmd.Flags().StringVar(&query, "query", "", "Go RE2 regex matched against string fields")
	cmd.Flags().StringVar(&field, "field", "", "restrict --query to one string field")
	return cmd
}

func renderSearchHits(w io.Writer, path string, hits []mcpsrv.SearchHit) error {
	r := render.New(w)
	if len(hits) == 0 {
		return r.Notice(laslig.NoticeInfoLevel, "search", "no hits", nil)
	}
	resolution, err := mcpsrv.ResolveProject(path)
	if err != nil {
		return fmt.Errorf("resolve schema: %w", err)
	}
	for _, hit := range hits {
		_, typeSt, err := lookupDBAndType(resolution.Registry, hit.Section)
		if err != nil {
			// Best-effort: render without typed fields.
			if err := r.Record(hit.Section, nil); err != nil {
				return err
			}
			continue
		}
		fields := make([]render.RenderField, 0, len(hit.Fields))
		for name, f := range typeSt.Fields {
			if v, ok := hit.Fields[name]; ok {
				fields = append(fields, render.RenderField{
					Name:  name,
					Type:  f.Type,
					Value: v,
				})
			}
		}
		render.SortFieldsByName(fields)
		if err := r.Record(hit.Section, fields); err != nil {
			return err
		}
	}
	return nil
}

// ---- helpers (CLI-local mirrors of the MCP handlers) -----------------

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
	resolution, err := mcpsrv.ResolveProject(path)
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
	return renderSchemaMarkdown(w, path, scope, resolution.Sources, dbs)
}

// renderMetaSchema prints the embedded meta-schema TOML literal directly —
// glamour-rendering a raw TOML body would add no value and hurt
// copy-paste. This is the CLI counterpart to MCP's `schema(scope=
// "ta_schema")`.
func renderMetaSchema(w io.Writer) error {
	body := "# ta_schema — embedded meta-schema\n\n```toml\n" + schema.MetaSchemaTOML + "```\n"
	return render.New(w).Markdown(body)
}

func renderSchemaMarkdown(w io.Writer, path, section string, sources []string, dbs map[string]schema.DB) error {
	var sb strings.Builder
	if section != "" {
		fmt.Fprintf(&sb, "# Schema for `%s` (section `%s`)\n\n", path, section)
	} else {
		fmt.Fprintf(&sb, "# Schema for `%s`\n\n", path)
	}
	if len(sources) > 0 {
		sb.WriteString("**Resolved from:**\n\n")
		for _, s := range sources {
			fmt.Fprintf(&sb, "- `%s`\n", s)
		}
		sb.WriteString("\n")
	}
	dbNames := make([]string, 0, len(dbs))
	for n := range dbs {
		dbNames = append(dbNames, n)
	}
	sort.Strings(dbNames)
	for _, dbName := range dbNames {
		dbDecl := dbs[dbName]
		fmt.Fprintf(&sb, "## `%s`\n\n", dbName)
		fmt.Fprintf(&sb, "- **shape**: `%s`\n- **path**: `%s`\n- **format**: `%s`\n\n",
			dbDecl.Shape, dbDecl.Path, dbDecl.Format)
		if dbDecl.Description != "" {
			sb.WriteString(dbDecl.Description + "\n\n")
		}
		typeNames := make([]string, 0, len(dbDecl.Types))
		for n := range dbDecl.Types {
			typeNames = append(typeNames, n)
		}
		sort.Strings(typeNames)
		for _, tname := range typeNames {
			t := dbDecl.Types[tname]
			fmt.Fprintf(&sb, "### `%s.%s`\n\n", dbName, tname)
			if t.Heading != 0 {
				fmt.Fprintf(&sb, "- **heading**: `%d`\n\n", t.Heading)
			}
			if t.Description != "" {
				sb.WriteString(t.Description + "\n\n")
			}
			sb.WriteString("| field | type | required | default | description |\n")
			sb.WriteString("|---|---|---|---|---|\n")
			fieldNames := make([]string, 0, len(t.Fields))
			for fn := range t.Fields {
				fieldNames = append(fieldNames, fn)
			}
			sort.Strings(fieldNames)
			for _, fn := range fieldNames {
				f := t.Fields[fn]
				req := ""
				if f.Required {
					req = "yes"
				}
				def := ""
				if f.Default != nil {
					def = fmt.Sprintf("`%v`", f.Default)
				}
				desc := strings.ReplaceAll(f.Description, "|", `\|`)
				fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s | %s |\n", fn, f.Type, req, def, desc)
			}
			sb.WriteString("\n")
		}
	}
	return render.New(w).Markdown(sb.String())
}

func noticeMutation(w io.Writer, action, section, filePath string, sources []string) error {
	body := section
	if filePath != "" {
		body = section + "\n" + filePath
	}
	return render.New(w).Success(action, body, sources)
}

// runCreate / runUpdate / runDelete / runSchemaMutate are thin
// wrappers over the shared mcpsrv.* Ops. Keeping them here means the
// CLI's error surface is pure-Go (no MCP envelope) while the MCP
// handlers in internal/mcpsrv/tools.go reuse exactly the same paths.

func runCreate(path, section, pathHint string, data map[string]any) (string, []string, error) {
	return mcpsrv.Create(path, section, pathHint, data)
}

func runUpdate(path, section string, data map[string]any) (string, []string, error) {
	return mcpsrv.Update(path, section, data)
}

func runDelete(path, section string) (string, []string, error) {
	return mcpsrv.Delete(path, section)
}

func runSchemaMutate(path, action, kind, name string, data map[string]any) ([]string, error) {
	return mcpsrv.MutateSchema(path, action, kind, name, data)
}
