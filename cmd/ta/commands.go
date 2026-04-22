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
	"github.com/evanmschultz/ta/internal/schema"
)

// newGetCmd mirrors the MCP tool `get`. Without --fields the CLI
// writes the raw on-disk bytes to stdout. With --fields (comma or
// repeated) it renders the named field values through laslig as a
// JSON object.
func newGetCmd() *cobra.Command {
	var fields []string
	cmd := &cobra.Command{
		Use:   "get <path> <section>",
		Short: "Read one record; optionally extract declared field values",
		Long: "Mirrors the MCP tool `get`. Default writes the raw bytes of the " +
			"located record to stdout. With --fields name[,name...] or repeated " +
			"--field <name> the CLI decodes the record and prints the named " +
			"field values as JSON.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, section := args[0], args[1]
			res, err := mcpsrv.Get(path, section, fields)
			if err != nil {
				return err
			}
			if len(fields) == 0 {
				_, err := c.OutOrStdout().Write(res.Bytes)
				return err
			}
			data, err := json.MarshalIndent(res.Fields, "", "  ")
			if err != nil {
				return fmt.Errorf("encode fields: %w", err)
			}
			p := laslig.New(c.OutOrStdout(), humanPolicy())
			return p.Markdown(laslig.Markdown{Body: "```json\n" + string(data) + "\n```\n"})
		},
	}
	cmd.Flags().StringSliceVar(&fields, "fields", nil, "comma-separated declared field names to extract")
	cmd.Flags().StringSliceVar(&fields, "field", nil, "declared field name to extract (repeatable)")
	return cmd
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
			items := make([]laslig.ListItem, len(paths))
			for i, sp := range paths {
				items[i] = laslig.ListItem{Title: sp}
			}
			p := laslig.New(c.OutOrStdout(), humanPolicy())
			return p.List(laslig.List{
				Title: path,
				Items: items,
				Empty: "(no sections)",
			})
		},
	}
}

func newCreateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	var pathHint string
	cmd := &cobra.Command{
		Use:   "create <path> <section>",
		Short: "Create a new record (fails if it exists); mirrors MCP tool `create`.",
		Long: "Create a new record at the given address. Fails if the record " +
			"already exists (V2-PLAN §3.4). Creates the backing file and any " +
			"intermediate directories on first use. For file-per-instance dbs, " +
			"--path-hint disambiguates flat vs nested placement.",
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
			return noticeMutation(c.OutOrStdout(), "created", section, targetPath, sources)
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin")
	cmd.Flags().StringVar(&pathHint, "path-hint", "", "relative placement hint inside a collection db's root")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	return cmd
}

func newUpdateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	cmd := &cobra.Command{
		Use:   "update <path> <section>",
		Short: "Update an existing record; mirrors MCP tool `update`.",
		Long: "Update an existing record. Fails if the backing file does not " +
			"exist (V2-PLAN §3.5). Creates the record within the file if the " +
			"file exists but the record does not (record-level upsert).",
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
			return noticeMutation(c.OutOrStdout(), "updated", section, targetPath, sources)
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin")
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
			return noticeMutation(c.OutOrStdout(), "schema "+action, name, "", sources)
		},
	}
	cmd.Flags().StringVar(&action, "action", "get", "one of get | create | update | delete")
	cmd.Flags().StringVar(&kind, "kind", "", "db | type | field (for action != get)")
	cmd.Flags().StringVar(&name, "name", "", "dotted schema address (for action != get)")
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON payload (for action create|update)")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON payload from file; use `-` for stdin")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	return cmd
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
	p := laslig.New(w, humanPolicy())
	body := "# ta_schema — embedded meta-schema\n\n```toml\n" + schema.MetaSchemaTOML + "```\n"
	return p.Markdown(laslig.Markdown{Body: body})
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
	p := laslig.New(w, humanPolicy())
	return p.Markdown(laslig.Markdown{Body: sb.String()})
}

func noticeMutation(w io.Writer, action, section, filePath string, sources []string) error {
	p := laslig.New(w, humanPolicy())
	body := section
	if filePath != "" {
		body = section + "\n" + filePath
	}
	return p.Notice(laslig.Notice{
		Level:  laslig.NoticeSuccessLevel,
		Title:  action,
		Body:   body,
		Detail: sources,
	})
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
