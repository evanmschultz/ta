package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/huh/v2"
	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/index"
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
// An id prefix (e.g. `plans` to enumerate every record in plans.toml)
// returns every matching record in file-parse order; --limit (default
// 10, -n shorthand) and --all control the cap. Full ids silently ignore
// --limit / --all.
func newGetCmd() *cobra.Command {
	var fields []string
	var asJSON bool
	var limit int
	var all bool
	var typeName string
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Read one record by id, or every record under an id prefix; optionally extract declared field values",
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
			"exclusive. With --json the laslig path is bypassed and JSON is " +
			"written for agent consumption. --path defaults to cwd; relative " +
			"or absolute accepted.",
		Example: "  ta get plans.task-001\n" +
			"  ta get --path /abs/proj plans.task-001 --fields status,body\n" +
			"  ta get plans.task-001 --json\n" +
			"  ta get plans --all --json\n" +
			"  ta get plans --limit 5",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			id := args[0]
			isScope, err := ops.IsScopeAddress(path, id)
			if err != nil {
				return err
			}
			if isScope {
				return runGetScope(c, path, id, fields, limit, all, asJSON)
			}
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
		},
	}
	cmd.Flags().StringSliceVar(&fields, "fields", nil, "comma-separated declared field names to extract")
	cmd.Flags().StringSliceVar(&fields, "field", nil, "declared field name to extract (repeatable)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "cap the record count at N when <id> is a prefix (default 10; ignored for full ids; mutually exclusive with --all)")
	cmd.Flags().BoolVar(&all, "all", false, "return every record when <id> is a prefix (ignored for full ids; mutually exclusive with --limit)")
	cmd.Flags().StringVar(&typeName, "type", "", "optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id")
	cmd.MarkFlagsMutuallyExclusive("limit", "all")
	addPathFlag(cmd)
	return cmd
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

// renderVerboseRecord fetches the named record and renders its bytes
// via renderRawRecord. Used by the --verbose flag on create / update
// to echo the post-mutation record content after the success notice
// per V2-PLAN §13.1. Returns any fetch error so the caller can surface
// it rather than silently skip the echo.
func renderVerboseRecord(w io.Writer, path, id string) error {
	res, err := ops.Get(path, id, "", nil)
	if err != nil {
		return fmt.Errorf("verbose echo: %w", err)
	}
	return renderRawRecord(render.New(w), path, id, res.Bytes)
}

// renderRawRecord routes an unparsed record through glamour. TOML bytes
// are wrapped in a ```toml fence so code highlighting survives; MD bytes
// are passed through unchanged because they're already markdown.
func renderRawRecord(r *render.Renderer, path, id string, raw []byte) error {
	format, err := dbFormatFor(path, id)
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

// dbFormatFor looks up the db format for the id's mount.
// Used to pick a render branch (TOML fenced vs MD pass-through).
func dbFormatFor(path, id string) (schema.Format, error) {
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return "", err
	}
	resolver := db.NewResolver(path, resolution.Registry)
	_, dbDecl, err := resolver.ResolveID(id)
	if err != nil {
		return "", fmt.Errorf("id %q: %w", id, err)
	}
	return dbDecl.Format, nil
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

// lookupDBAndType resolves the db + type for an id by routing through
// the resolver and consulting the index for the authoritative type.
// Falls back to an arbitrary declared type when the index has no entry
// (best-effort render path).
func lookupDBAndType(reg schema.Registry, projectPath, id string) (schema.DB, schema.SectionType, error) {
	resolver := db.NewResolver(projectPath, reg)
	resolved, dbDecl, err := resolver.ResolveID(id)
	if err != nil {
		return schema.DB{}, schema.SectionType{}, fmt.Errorf("id %q: %w", id, err)
	}
	// Per F10 the type lives in the index. Best-effort: load the
	// index, look up the canonical id, and use the recorded type.
	if idx, ierr := index.Load(projectPath); ierr == nil {
		if entry, ok := idx.Get(resolved.Canonical()); ok {
			if t, ok := dbDecl.Types[entry.Type]; ok {
				return dbDecl, t, nil
			}
		}
	}
	// Fall back to the first declared type so render still works.
	for _, t := range dbDecl.Types {
		return dbDecl, t, nil
	}
	return dbDecl, schema.SectionType{}, fmt.Errorf("db %q has no declared types", dbDecl.Name)
}

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

func newCreateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	var typeName string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a new record (fails if it exists); mirrors MCP tool `create`.",
		Long: "Create a new record at the given id. Fails if the record " +
			"already exists. Creates the backing file and any intermediate " +
			"directories on first use. --type is REQUIRED and must be " +
			"db-qualified (`<db>.<type>`, e.g. `plans.task`). With " +
			"--verbose, the newly-created record content is echoed after " +
			"the success notice. --path defaults to cwd; relative or " +
			"absolute accepted.",
		Example: "  ta create plans.task-001 --type plans.task --data '{\"id\":\"task-001\",\"status\":\"todo\"}'\n" +
			"  ta create --path /abs/proj plans.task-001 --type plans.task --data-file payload.json\n" +
			"  cat payload.json | ta create plans.task-001 --type plans.task --data-file -",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			id := args[0]
			data, err := collectCreateData(c, path, id, dataInline, dataFile)
			if err != nil {
				return err
			}
			targetPath, sources, err := runCreate(path, id, typeName, data)
			if err != nil {
				return err
			}
			if err := noticeMutation(c.OutOrStdout(), "created", id, targetPath, sources); err != nil {
				return err
			}
			if verbose {
				return renderVerboseRecord(c.OutOrStdout(), path, id)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin")
	cmd.Flags().StringVar(&typeName, "type", "", "REQUIRED db-qualified type (`<db>.<type>`, e.g. `plans.task`)")
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
		Use:   "update <id>",
		Short: "PATCH an existing record; mirrors MCP tool `update`.",
		Long: "PATCH-style update: --data is a partial overlay, not a full " +
			"replacement. Provided fields overwrite their stored values; " +
			"unspecified fields keep their bytes verbatim. Empty --data ({}) " +
			"is a no-op success. Null on a non-required field clears it; " +
			"null on a required field with a schema default resets it to " +
			"that default; null on a required field with no default errors. " +
			"The merged record is atomically re-validated. Fails if the " +
			"backing file does not exist; creates the record within the " +
			"file when absent (record-level upsert). With --verbose, the " +
			"updated record is echoed after the success notice. --path " +
			"defaults to cwd; relative or absolute accepted.",
		Example: "  ta update plans.task-001 --data '{\"status\":\"done\"}'\n" +
			"  ta update plans.task-001 --data '{\"notes\":null}'    # clear optional field\n" +
			"  ta update --path /abs/proj plans.task-001 --data-file patch.json --verbose",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			id := args[0]
			data, err := collectUpdateData(c, path, id, dataInline, dataFile)
			if err != nil {
				return err
			}
			targetPath, sources, err := runUpdate(path, id, typeName, data)
			if err != nil {
				return err
			}
			if err := noticeMutation(c.OutOrStdout(), "updated", id, targetPath, sources); err != nil {
				return err
			}
			if verbose {
				return renderVerboseRecord(c.OutOrStdout(), path, id)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin")
	cmd.Flags().StringVar(&typeName, "type", "", "optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the updated record after the success notice")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	addPathFlag(cmd)
	return cmd
}

func newDeleteCmd() *cobra.Command {
	var typeName string
	var force bool
	var verbose bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Remove a record or file; mirrors MCP tool `delete`.",
		Long: "Remove a record (bytes spliced out) by full id, or remove a " +
			"whole file by passing a bare file-relpath that uniquely " +
			"identifies one concrete file. A file-relpath that resolves " +
			"through a glob mount to multiple concrete files refuses with " +
			"an unscoped-glob error. File-level delete prompts for " +
			"confirmation on a TTY; pass --force to skip the prompt for " +
			"non-interactive callers. --verbose echoes the deleted id, the " +
			"absolute file it lived in, and the count of records remaining " +
			"in that file. --type is optional and cross-checks the " +
			"supplied type against the index entry for the id. --path " +
			"defaults to cwd; relative or absolute accepted.",
		Example: `  ta delete plans.task-001
  ta delete --force plans
  ta delete --verbose plans.task-001
  ta delete workflow.drop-3.db.task-001`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			id := args[0]
			res, err := runDelete(path, id, typeName, ops.DeleteOptions{Force: force, Verbose: verbose})
			if err == nil || !errors.Is(err, ops.ErrFileDeleteRequiresForce) {
				if err != nil {
					return err
				}
				return emitDeleteNotice(c.OutOrStdout(), id, res, verbose)
			}
			// File-level delete needs Force; on TTY, surface a
			// huh.Confirm and (on yes) retry with Force=true. Off-TTY,
			// the original ErrFileDeleteRequiresForce surfaces.
			if !ttyInteractive(false) {
				return err
			}
			ok, confirmErr := confirmFileDelete(id, res.FilePath)
			if confirmErr != nil {
				return confirmErr
			}
			if !ok {
				return fmt.Errorf("delete %q cancelled", id)
			}
			res, err = runDelete(path, id, typeName, ops.DeleteOptions{Force: true, Verbose: verbose})
			if err != nil {
				return err
			}
			return emitDeleteNotice(c.OutOrStdout(), id, res, verbose)
		},
	}
	cmd.Flags().StringVar(&typeName, "type", "", "optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id")
	cmd.Flags().BoolVar(&force, "force", false, "skip the interactive confirmation prompt on file-level delete")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the deleted id, its file path, and the count of records remaining in that file")
	addPathFlag(cmd)
	return cmd
}

// confirmFileDelete prompts the user to confirm a file-level delete.
// Wraps a huh.Confirm so the prompt body matches the existing
// confirmOverwrite shape used by `ta init` (consistent visual idiom).
func confirmFileDelete(id, filePath string) (bool, error) {
	var ok bool
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Delete entire file %s (id=%q)?", filePath, id)).
			Value(&ok),
	))
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("confirm prompt: %w", err)
	}
	return ok, nil
}

// emitDeleteNotice renders the post-delete laslig SUCCESS notice. The
// non-verbose form mirrors noticeMutation; with --verbose the body
// adds two lines: the absolute file path and the count of records
// remaining in that file.
func emitDeleteNotice(w io.Writer, id string, res ops.DeleteResult, verbose bool) error {
	if !verbose {
		return noticeMutation(w, "deleted", id, res.FilePath, res.Sources)
	}
	body := fmt.Sprintf("%s\n%s\nremaining in file: %d", id, res.FilePath, res.RemainingInFile)
	return render.New(w).Success("deleted", body, res.Sources)
}

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
				if asJSON {
					return runSchemaGetJSON(c.OutOrStdout(), path, scope)
				}
				return runSchemaGet(c.OutOrStdout(), path, scope)
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

// ---- helpers (CLI-local mirrors of the MCP handlers) -----------------

// collectCreateData is the create-side entrypoint for field data.
// Preserves the non-interactive --data / --data-file contract and, when
// neither is set and stdin is a TTY, runs the interactive huh form
// built from the resolved type's declared fields. Off-TTY with no flags
// errors politely so agents and scripts fail loudly instead of hanging
// on stdin.
func collectCreateData(c *cobra.Command, path, id, dataInline, dataFile string) (map[string]any, error) {
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
	typeSt, err := resolveTypeForID(path, id)
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
// semantics.
func collectUpdateData(c *cobra.Command, path, id, dataInline, dataFile string) (map[string]any, error) {
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
	res, typeSt, err := ops.GetAllFields(path, id, "")
	if err != nil {
		return nil, err
	}
	form, _, collect := FormFor(typeSt, res.Fields, true)
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("form: %w", err)
	}
	return collect()
}

// resolveTypeForID returns the SectionType that the id names, resolving
// the db + type from the project registry. Used by the create path,
// which cannot rely on an existing record for schema lookup.
func resolveTypeForID(path, id string) (schema.SectionType, error) {
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return schema.SectionType{}, fmt.Errorf("resolve schema: %w", err)
	}
	_, typeSt, err := lookupDBAndType(resolution.Registry, path, id)
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

func noticeMutation(w io.Writer, action, id, filePath string, sources []string) error {
	body := id
	if filePath != "" {
		body = id + "\n" + filePath
	}
	return render.New(w).Success(action, body, sources)
}

// runCreate / runUpdate / runDelete / runSchemaMutate are thin
// wrappers over the shared ops.* endpoints. Keeping them here means the
// CLI's error surface is pure-Go (no MCP envelope) while the MCP
// handlers in internal/mcpsrv/tools.go reuse exactly the same paths.

func runCreate(path, id, typeName string, data map[string]any) (string, []string, error) {
	return ops.Create(path, id, typeName, data)
}

func runUpdate(path, id, typeName string, data map[string]any) (string, []string, error) {
	return ops.Update(path, id, typeName, data)
}

// runDelete is the F19/F20 entry point used by `ta delete`. It threads
// DeleteOptions (Force / Verbose) into the ops layer and returns the
// structured DeleteResult so the CLI can render the verbose-mode
// "remaining in file" line without re-loading the index.
func runDelete(path, id, typeName string, opts ops.DeleteOptions) (ops.DeleteResult, error) {
	return ops.DeleteWithOptions(path, id, typeName, opts)
}

func runSchemaMutate(path, action, kind, name string, data map[string]any) ([]string, error) {
	return ops.MutateSchema(path, action, kind, name, data)
}
