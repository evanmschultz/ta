package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/schema"
)

func newSchemaCmd() *cobra.Command {
	var action string
	var kind string
	var name string
	var dataInline string
	var dataFile string
	var pathsAppend string
	var pathsRemove string
	var verbose bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "schema [scope]",
		Short: "Inspect or mutate the resolved schema; mirrors MCP tool `schema`.",
		Long: "With action=get (default), renders the resolved schema; an " +
			"optional scope (`<db>` or `<db>.<type>`) narrows to one db or " +
			"type. Passing the reserved value `ta_schema` prints the " +
			"embedded meta-schema literal. With action=create|update|delete, " +
			"mutates the project `.ta/schema.toml` (re-validated on every " +
			"mutation with atomic rollback). With action=update + kind=db, " +
			"the --paths-append=<entry> / --paths-remove=<entry> sugar " +
			"mutates the db's `paths` slice incrementally; each flag takes " +
			"one entry and the two are mutually exclusive with each other " +
			"and with `--data` carrying a `paths` key. With --json the " +
			"laslig path is bypassed and JSON is written for agent " +
			"consumption (action=get only; mutations always print the " +
			"success notice). --path defaults to cwd; relative or absolute " +
			"accepted.",
		Example: `  ta schema
  ta schema plans.task --json
  ta schema ta_schema
  ta schema --path /abs/proj --action=create --kind=type --name=plans.note --data '{...}'
  ta schema --action=update --kind=db --name=plans --paths-append=archive
  ta schema --action=update --kind=db --name=plans --paths-remove=archive`,
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
				// Scoped wrap: --json is the documented contract for
				// action=get ONLY (schema doc §). Mutations (create /
				// update / delete) always emit laslig notices, so a
				// full-RunE wrap would create asymmetric output where
				// mutation success is laslig but mutation error is a
				// JSON envelope. Wrap inside this branch instead.
				return runWithJSONErrEnvelope(c, asJSON, func() error {
					if asJSON {
						return runSchemaGetJSON(c.OutOrStdout(), path, scope)
					}
					return runSchemaGet(c.OutOrStdout(), path, scope)
				})
			}
			// PLAN §12.17.9 Phase 9.6: --paths-append / --paths-remove
			// sugar lives strictly on action=update + kind=db. Cobra's
			// MarkFlagsMutuallyExclusive handles append-vs-remove and
			// each-vs-data; we still gate on action+kind so misuse on
			// e.g. action=create surfaces a clear scope error rather
			// than silently colliding with the create payload.
			if pathsAppend != "" || pathsRemove != "" {
				if action != "update" || kind != "db" {
					return fmt.Errorf("--paths-append / --paths-remove only valid with --action=update --kind=db")
				}
				if name == "" {
					return errors.New("schema: missing required --name")
				}
				sources, err := ops.MutateDBPaths(path, name, pathsAppend, pathsRemove)
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
	cmd.Flags().StringVar(&kind, "kind", "", "db | type | field | base (for action != get)")
	cmd.Flags().StringVar(&name, "name", "", "dotted schema address (for action != get)")
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON payload (for action create|update)")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON payload from file; use `-` for stdin")
	cmd.Flags().StringVar(&pathsAppend, "paths-append", "", "append one entry to a db's paths slice (action=update --kind=db); idempotent — repeats are no-ops")
	cmd.Flags().StringVar(&pathsRemove, "paths-remove", "", "remove one entry from a db's paths slice (action=update --kind=db); missing entries are no-ops")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the post-mutation schema after the success notice (no effect on action=get)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output (action=get)")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	cmd.MarkFlagsMutuallyExclusive("paths-append", "paths-remove")
	cmd.MarkFlagsMutuallyExclusive("paths-append", "data")
	cmd.MarkFlagsMutuallyExclusive("paths-append", "data-file")
	cmd.MarkFlagsMutuallyExclusive("paths-remove", "data")
	cmd.MarkFlagsMutuallyExclusive("paths-remove", "data-file")
	addPathFlag(cmd)
	return cmd
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
				return fmt.Errorf("no schema registered for scope %q in %s", scope, path)
			}
		} else {
			return fmt.Errorf("no schema registered for scope %q in %s", scope, path)
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
				return fmt.Errorf("no schema registered for scope %q in %s", scope, path)
			}
		} else {
			return fmt.Errorf("no schema registered for scope %q in %s", scope, path)
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
