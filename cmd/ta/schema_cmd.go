package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	// Blank imports register the html / md / txt Format engines with the
	// format substrate. Mirrors cmd/ta/get_cmd.go (L3-D5-D1) — each
	// read-side _cmd.go that consumes format.Get anchors the registration
	// at its own callsite so the format-dispatch dependency stays
	// explicit per-command. L3-D5-D3 lights up the read side of `ta
	// schema --action=get --as=<fmt>`. L3-D5-D4 (write side, blocked on
	// this droplet) re-uses the SAME --as flag declared at cmd level
	// here; it does not redeclare the flag.
	_ "github.com/evanmschultz/ta/internal/backend/html"
	_ "github.com/evanmschultz/ta/internal/backend/md_explicit"
	_ "github.com/evanmschultz/ta/internal/backend/txt"

	"github.com/evanmschultz/ta/internal/format"
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
	// L3-D5-D3 (read) + L3-D5-D4 (write) format-substrate render hint.
	// --as picks the format engine name (html | md | txt); when set on
	// action=get the resolved schema bytes route through
	// format.Get(<name>).Marshal before emit. When unset, the existing
	// laslig path runs unchanged. D5-D4 (write side, blocked on this
	// droplet) re-uses this SAME flag declaration on the mutating action
	// branches (no second declaration). --template is intentionally NOT
	// declared here per planner CE-I: schema-read has no manifest binding
	// in this slice.
	var asFormat string
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
					// L3-D5-D3 read-side dispatch: when --as is set, the
					// resolved schema bytes route through the format
					// substrate before emit. This gate fires ONLY inside
					// the action=get branch — D5-D4 (write side) installs
					// its own gate against the same `asFormat` variable in
					// the mutation branches below.
					if asFormat != "" {
						return runSchemaGetWithFormat(c.OutOrStdout(), path, scope, asFormat, asJSON)
					}
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
			// L3-D5-D4 write-side dispatch: when --as is set on a
			// mutating action (create | update), the --data / --data-file
			// bytes route through format.Get(asFormat).Parse before the
			// schema mutation runs. Mirrors create_cmd.go's
			// runCreateSingleWithFormat (D5-D5) and update_cmd.go's
			// validateUpdateAsFormat (D5-D6). Delete carries no payload
			// to parse, so --as has no semantic — reject loudly rather
			// than silently ignore.
			if asFormat != "" && action == "delete" {
				return fmt.Errorf("ta schema: --as is not supported with --action=delete (no payload to parse)")
			}
			if asFormat != "" && (action == "create" || action == "update") {
				return runSchemaMutateWithFormat(c, path, action, kind, name, dataInline, dataFile, asFormat, verbose)
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
	// L3-D5-D3 (read) + L3-D5-D4 (write) shared declaration. Touch ONCE
	// here so the same `asFormat` binding feeds both action=get's read
	// dispatch (above) and action=create|update|delete's write dispatch
	// (D5-D4, blocked on this droplet). Per planner CE-I, no --template
	// flag this slice.
	cmd.Flags().StringVar(&asFormat, "as", "", "format engine name (html | md | txt); routes schema bytes through format.Get(name).Marshal before emit (default: db's on-disk format)")
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

// runSchemaGetWithFormat is the L3-D5-D3 read-side mirror of
// cmd/ta/get_cmd.go::runGetSingleWithFormat. It applies the same 4-step
// shape to `ta schema --action=get`:
//
//  1. Get raw schema dump bytes (concatenated source TOML files —
//     resolution.Sources is the canonical source list).
//  2. Resolve effectiveAs (defaults to the relevant db's on-disk
//     Format string when --as is unset; --as overrides).
//  3. Validate db.Format / --as match per the same planner-pinned
//     contract used by `ta get`: when --as is set explicitly AND
//     differs from string(db.Format), error with the shape
//     "db.Format=<x>; --as=<y> requires matching format".
//  4. Resolve the format engine via format.Get; Parse → Marshal →
//     emit via laslig Markdown (or {id, bytes} JSON shape when --json).
//
// Per planner CE-I this slice does NOT add --template support. The
// engine runs with a nil manifest — backends interpret nil as "no
// manifest, no blocks", so the round-trip emits empty bytes for the
// schema TOML input. The success surface is what the positive test
// pins, not the byte content (mirroring get_cmd.go's
// TestGet_AsFormat_MD docstring).
//
// "Relevant db" selection: when scope names a db (or a db.type), that
// db's Format is the mismatch baseline. When scope is empty, the dbs
// are iterated in name-sorted order and the FIRST db's Format is used.
// Multi-db unscoped projects today are an existing edge — under this
// rule the deterministic "first by name" choice keeps the contract
// stable across map-iteration order. L3-D5-D4 (write side) will reuse
// the same dbFormatForSchemaScope helper.
func runSchemaGetWithFormat(w io.Writer, path, scope, asFormatName string, asJSON bool) error {
	// (1) raw bytes — concatenate every source file backing this
	// project's resolved schema. The format engine doesn't care that
	// it's TOML; with a nil manifest the engine emits empty output
	// regardless.
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	rawBytes, err := readSchemaSourceBytes(resolution.Sources)
	if err != nil {
		return fmt.Errorf("ta schema: read schema source: %w", err)
	}
	// (2) + (3) resolve effectiveAs + mismatch check.
	dbFormat, err := dbFormatForSchemaScope(resolution.Registry, scope)
	if err != nil {
		return err
	}
	effectiveAs := asFormatName
	if effectiveAs == "" {
		effectiveAs = string(dbFormat)
	}
	// Mismatch rule per planner contract — identical message shape to
	// `ta get`: planner-pinned "db.Format=<x>; --as=<y> requires
	// matching format".
	if asFormatName != "" && asFormatName != string(dbFormat) {
		return fmt.Errorf("db.Format=%s; --as=%s requires matching format", string(dbFormat), asFormatName)
	}
	// (4) engine resolve + Parse + Marshal + emit.
	engine, err := format.Get(effectiveAs)
	if err != nil {
		return fmt.Errorf("ta schema: --as: %w", err)
	}
	blocks, err := engine.Parse(rawBytes, nil)
	if err != nil {
		return fmt.Errorf("ta schema: parse: %w", err)
	}
	out, err := engine.Marshal(blocks, nil)
	if err != nil {
		return fmt.Errorf("ta schema: marshal: %w", err)
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		payload := map[string]any{
			"schema_paths": resolution.Sources,
			"bytes":        string(out),
		}
		if scope != "" {
			payload["scope"] = scope
		}
		return enc.Encode(payload)
	}
	return render.New(w).Markdown(string(out))
}

// readSchemaSourceBytes concatenates the on-disk schema source files
// into a single byte stream. The order matches resolution.Sources so
// the dump is deterministic. Used by L3-D5-D3 and reusable by D5-D4
// for the post-mutation "echo dumped via --as" case.
func readSchemaSourceBytes(sources []string) ([]byte, error) {
	var buf []byte
	for _, src := range sources {
		body, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", src, err)
		}
		if len(buf) > 0 && len(body) > 0 && buf[len(buf)-1] != '\n' {
			buf = append(buf, '\n')
		}
		buf = append(buf, body...)
	}
	return buf, nil
}

// dbFormatForSchemaScope picks the db whose Format drives the
// mismatch baseline for `ta schema --as=<fmt>`.
//
//   - scope = "" → first db in name-sorted order.
//   - scope = "<db>" → that db.
//   - scope = "<db>.<type>" → the db owning that type.
//   - scope = ta_schema → schema.FormatTOML (the embedded meta-schema is
//     TOML on disk).
//
// Error when scope is set but no db matches (preserves the existing
// `runSchemaGet` "no schema registered for scope" error shape). When
// no dbs are registered at all the registry is malformed; surface a
// clear error rather than panic. D5-D4 reuses this helper.
func dbFormatForSchemaScope(reg schema.Registry, scope string) (schema.Format, error) {
	if scope == schema.MetaSchemaPath {
		return schema.FormatTOML, nil
	}
	if scope != "" {
		if _, ok := reg.Lookup(scope); ok {
			dbDecl, _ := reg.LookupDB(scope)
			return dbDecl.Format, nil
		}
		if !strings.Contains(scope, ".") {
			if dbDecl, ok := reg.LookupDB(scope); ok {
				return dbDecl.Format, nil
			}
		}
		return "", fmt.Errorf("no schema registered for scope %q", scope)
	}
	names := make([]string, 0, len(reg.DBs))
	for name := range reg.DBs {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", errors.New("ta schema: no dbs registered")
	}
	sort.Strings(names)
	return reg.DBs[names[0]].Format, nil
}

// runSchemaMutateWithFormat is the L3-D5-D4 WRITE-side mirror of
// runSchemaGetWithFormat (D5-D3). It applies the same 4-step shape to
// `ta schema --action=create|update`:
//
//  1. Require --data or --data-file (schema mutations on the format
//     path expect raw bytes — no TTY interactive form exists for the
//     mutating schema branch and consuming nil bytes would silently
//     emit an empty payload).
//  2. Resolve the relevant db.Format for the schema target via
//     dbFormatForSchemaScope (D5-D3 helper). For kind=db the target is
//     `<db>`; for kind=type / kind=field the target is the dotted
//     name's leading `<db>` (or `<db>.<type>` for type-kind), which
//     dbFormatForSchemaScope already resolves correctly.
//  3. Mismatch check: explicit --as != string(dbFormat) errors with the
//     planner-pinned message shape `"db.Format=<x>; --as=<y> requires
//     matching format"` (identical to D5-D3 read side and D5-D5/D5-D6
//     write sides — cross-command consistency).
//  4. Resolve the engine via format.Get; Parse(raw, nil) → blocks. The
//     blocks → data map projection is `block.Name → string(block.Bytes)`,
//     mirroring D5-D5 (runCreateSingleWithFormat). With nil manifest the
//     engine returns an empty Blocks slice; the resulting empty data map
//     is then handed to ops.MutateSchema which surfaces a clear schema-
//     mutation rejection — a correct validation surface, not a bug. A
//     post-MVP --template counterpart will fill blocks under a real
//     manifest.
func runSchemaMutateWithFormat(c *cobra.Command, path, action, kind, name, dataInline, dataFile, asFormat string, verbose bool) error {
	if dataInline == "" && dataFile == "" {
		return fmt.Errorf("ta schema: --as requires --data or --data-file (no interactive form on the format path)")
	}
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	// Scope for the mismatch baseline: the schema target's leading db.
	// For kind=db, name == "<db>". For kind=type / kind=field, the
	// dotted name's first segment is the db; dbFormatForSchemaScope
	// already handles `<db>` and `<db>.<type>` forms — for field-kind
	// the `<db>.<type>.<field>` form is trimmed to its `<db>.<type>`
	// prefix so the same helper resolves the right db.
	scope := schemaTargetScope(kind, name)
	dbFormat, err := dbFormatForSchemaScope(resolution.Registry, scope)
	if err != nil {
		return err
	}
	if asFormat != string(dbFormat) {
		return fmt.Errorf("db.Format=%s; --as=%s requires matching format", string(dbFormat), asFormat)
	}
	engine, err := format.Get(asFormat)
	if err != nil {
		return fmt.Errorf("ta schema: --as=%s: %w", asFormat, err)
	}
	raw, err := readJSONData(dataInline, dataFile, c.InOrStdin())
	if err != nil {
		return err
	}
	blocks, err := engine.Parse(raw, nil)
	if err != nil {
		return fmt.Errorf("ta schema: parse --data as %s: %w", asFormat, err)
	}
	// Block.Name → field key, string(Block.Bytes) → field value.
	// Mirrors the WRITE-side projection established in D5-D5
	// (runCreateSingleWithFormat). For schema mutations the resulting
	// map is the registry-shaped payload ops.MutateSchema expects
	// (`paths`, `description`, etc. on kind=db; `description`, `heading`,
	// `fields` on kind=type). With nil manifest the engine yields empty
	// blocks → empty data, which the schema-mutation handler surfaces as
	// a clear validation rejection. The contract pinned here is the
	// success/failure surface, not the round-trip content — a real
	// --template (post-MVP) lights up the byte-content path.
	data := make(map[string]any, len(blocks))
	for _, b := range blocks {
		data[b.Name] = string(b.Bytes)
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
}

// schemaTargetScope returns the dbFormatForSchemaScope-compatible scope
// for a schema mutation target. The returned scope always names an
// EXISTING db (never a not-yet-created type/field/base) so the
// db.Format mismatch baseline lookup is well-defined even on create
// actions where the named target itself does not yet exist.
//
//   - kind=db → name unchanged (`<db>` — the db itself; the mismatch
//     baseline is the existing db's Format on update / first-db
//     fallback on create where the target db does not yet exist).
//   - kind=type → first segment of name (the owning `<db>` — the type
//     may not yet exist on create).
//   - kind=field → first segment of name (the owning `<db>` — the type
//     and field may not yet exist on create).
//   - kind=base → first segment of name (the owning `<db>`).
//
// For kind=db on action=create where the db does not yet exist,
// dbFormatForSchemaScope's "no schema registered for scope" error is
// the correct surface — the caller cannot match a format against a
// db that hasn't been declared. Today's substrate has db.Format
// inferred from `paths` extensions; pre-create-of-the-db the format
// has no source. Schema-create of a new db with --as is therefore
// not a supported combination — operators create the db first via
// the no-format path, then mutate its types/fields with --as.
func schemaTargetScope(kind, name string) string {
	switch kind {
	case "db":
		return name
	default:
		// kind=type / kind=field / kind=base / unknown: target the
		// owning db (first dotted segment).
		if i := strings.IndexByte(name, '.'); i >= 0 {
			return name[:i]
		}
		return name
	}
}
