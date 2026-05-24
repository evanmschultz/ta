package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/fsatomic"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/schema"
	"github.com/evanmschultz/ta/internal/templates"
)

// newTemplateCmd is the parent for `ta template *`. Children are the
// read-only `list` / `show` pair plus the write-side
// `save` / `apply` / `delete` trio. Every child honors the same
// TTY-vs-flag discipline as `ta init`.
//
// Post-F15 the home library is a single `~/.ta/schema.toml` file that
// aggregates dbs by name. Each subcommand operates on db-names rather
// than per-template files; legacy `~/.ta/<name>.toml` files are
// detected and warned about (no auto-migration).
func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Manage the ~/.ta/schema.toml template library",
		Long: "Inspect and manage the global schema template library at " +
			"`~/.ta/schema.toml`. The file aggregates one or more dbs by name; " +
			"`ta init` picks from this set to bootstrap a new project.\n\n" +
			"Children: `list` (enumerate dbs), `show <db>` (inspect one db), " +
			"`save [<db>...]` (merge dbs from `<cwd>/.ta/schema.toml` into " +
			"`~/.ta/schema.toml`), `apply <db> [--path <project>]` (extract " +
			"one db into a project schema), `delete <db>` (remove a db).",
		Example:       "  ta template list\n  ta template show plans\n  ta template save\n  ta template save plans notes\n  ta template apply plans\n  ta template delete plans",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(
		newTemplateListCmd(),
		newTemplateShowCmd(),
		newTemplateSaveCmd(),
		newTemplateApplyCmd(),
		newTemplateDeleteCmd(),
	)
	return cmd
}

// ---- list ------------------------------------------------------------

func newTemplateListCmd() *cobra.Command {
	var asJSON bool
	var kind string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List items in the template library (schema|agent|config|docs-template)",
		Long: "Default lists every db declared in `~/.ta/schema.toml`. With " +
			"`--kind=<kind>` enumerates the named category (`agent`, `config`, " +
			"`docs-template`) across BOTH binary `examples/` and home `~/.ta/` " +
			"sources, tagging each entry with its provenance. With `--kind=all` " +
			"every category is enumerated. JSON emit mirrors the structured " +
			"shape so agents can consume the result directly.",
		Example: "  ta template list\n  ta template list --kind=agent\n  ta template list --kind=all --json",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			// Legacy warning is a stderr notice — kept OUTSIDE the
			// JSON envelope wrap because the envelope is a stdout
			// contract for the agent-facing payload only. Stderr
			// notices pass through verbatim in both modes.
			emitLegacyWarning(c.ErrOrStderr())
			return runWithJSONErrEnvelope(c, asJSON, func() error {
				if kind == "" || kind == "schema" {
					return runTemplateListSchema(c.OutOrStdout(), asJSON)
				}
				return runTemplateListMulti(c.OutOrStdout(), kind, asJSON)
			})
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().StringVar(&kind, "kind", "", "category to list: schema (default) | agent | config | docs-template | all")
	return cmd
}

// runTemplateListSchema is the pre-F24 schema-only path, retained for
// backward compat with all existing tests and CLI users.
func runTemplateListSchema(out io.Writer, asJSON bool) error {
	names, err := templates.ListDBs()
	if err != nil {
		return err
	}
	if asJSON {
		if names == nil {
			names = []string{}
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"dbs": names})
	}
	root, err := templates.Root()
	if err != nil {
		return err
	}
	return render.New(out).List(root, names, "(no dbs)")
}

// runTemplateListMulti enumerates one (or all) non-schema categories
// across binary + home provenance.
func runTemplateListMulti(out io.Writer, kindFlag string, asJSON bool) error {
	var items []templates.Item
	switch kindFlag {
	case "agent":
		var err error
		items, err = templates.ListItems(templates.KindAgent)
		if err != nil {
			return err
		}
	case "config":
		var err error
		items, err = templates.ListItems(templates.KindConfig)
		if err != nil {
			return err
		}
	case "docs-template":
		var err error
		items, err = templates.ListItems(templates.KindDocsTemplate)
		if err != nil {
			return err
		}
	case "all":
		var err error
		items, err = templates.ListAll()
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown --kind %q (want schema|agent|config|docs-template|all)", kindFlag)
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(itemsJSONShape(items))
	}
	for _, it := range items {
		display := itemListDisplay(it)
		fmt.Fprintln(out, display)
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "(no items)")
	}
	return nil
}

// itemsJSONShape converts a templates.Item slice into the wire-stable
// JSON-friendly shape used by `--json` output and the MCP preview
// tool.
func itemsJSONShape(items []templates.Item) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		entry := map[string]any{
			"kind":       string(it.Kind),
			"name":       it.Name,
			"provenance": string(it.Provenance),
		}
		if it.Group != "" {
			entry["group"] = it.Group
		}
		if it.Description != "" {
			entry["description"] = it.Description
		}
		out = append(out, entry)
	}
	return out
}

// itemListDisplay formats one item as a single line for the laslig
// list rendering.
func itemListDisplay(it templates.Item) string {
	tag := "[" + string(it.Provenance) + "] " + string(it.Kind) + ":"
	if it.Group != "" {
		tag += it.Group + "/"
	}
	if it.Description == "" {
		return tag + it.Name
	}
	return tag + it.Name + " — " + it.Description
}

// emitLegacyWarning surfaces a one-line laslig warning on stderr when
// `~/.ta/` carries legacy per-template files alongside schema.toml.
// Per F15 design decision #4: warn-only, no auto-migration.
func emitLegacyWarning(errOut io.Writer) {
	files, err := templates.LegacyTemplateFiles()
	if err != nil || len(files) == 0 {
		return
	}
	root, _ := templates.Root()
	_ = render.New(errOut).Notice(
		laslig.NoticeWarningLevel,
		"legacy template files detected",
		fmt.Sprintf("ignoring %d legacy template file(s) in %s; merge into schema.toml or remove",
			len(files), root),
		legacyFileNames(files),
	)
}

func legacyFileNames(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// ---- show ------------------------------------------------------------

func newTemplateShowCmd() *cobra.Command {
	var asJSON bool
	var kind string
	var group string
	var provenance string
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Print the bytes of one item from the template library",
		Long: "Reads one item from the template library and renders or emits its " +
			"bytes. Default `--kind=schema` resolves a fragment name across binary " +
			"`examples/schemas/` and `~/.ta/schema.toml` — home wins by default, " +
			"`--provenance=ta` forces the binary fragment, `--provenance=home` " +
			"forces home only. `--kind=agent --group=<group>` resolves to either " +
			"`~/.ta/agents/<group>/<name>.md` (home) or " +
			"`examples/agents/<group>/<name>.md` (binary). `--kind=config` and " +
			"`--kind=docs-template` operate on the corresponding flat libraries. " +
			"`--provenance=ta|home` disambiguates when the same name exists on " +
			"both sources; default is to prefer home.",
		Example: "  ta template show plans\n  ta template show plans --kind=schema --provenance=ta\n  ta template show go-builder --kind=agent --group=go\n  ta template show CLAUDE --kind=docs-template --provenance=ta",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runWithJSONErrEnvelope(c, asJSON, func() error {
				name := args[0]
				if kind == "" || kind == "schema" {
					return runTemplateShowSchema(c.OutOrStdout(), name, provenance, asJSON)
				}
				return runTemplateShowItem(c.OutOrStdout(), name, kind, group, provenance, asJSON)
			})
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	cmd.Flags().StringVar(&kind, "kind", "", "item kind: schema (default) | agent | config | docs-template")
	cmd.Flags().StringVar(&group, "group", "", "agent subdir (kind=agent only)")
	cmd.Flags().StringVar(&provenance, "provenance", "", "source: ta (binary) | home (default home, fall back to binary)")
	return cmd
}

// runTemplateShowSchema resolves a schema fragment name with optional
// provenance pinning. Empty provenance means "prefer home, fall back
// to binary" — preserves the F15 default behavior. Explicit
// `--provenance=ta` reaches the binary library directly so binary-only
// fragments (e.g. `[ta] agents`) are reachable; `--provenance=home`
// reads strictly from `~/.ta/schema.toml`.
func runTemplateShowSchema(out io.Writer, name, provFlag string, asJSON bool) error {
	data, prov, err := resolveSchemaForShow(name, provFlag)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"db":         name,
			"provenance": string(prov),
			"bytes":      string(data),
		})
	}
	return renderTemplateBody(out, name, data)
}

// resolveSchemaForShow looks up one schema fragment honoring
// provenance pinning. Returns the bytes plus the resolved provenance
// so JSON callers can see which source actually answered.
func resolveSchemaForShow(name, provFlag string) ([]byte, templates.Provenance, error) {
	switch provFlag {
	case "":
		// Default: prefer home, fall back to binary. ShowDB is the
		// canonical home read; ShowItem with ProvenanceBinary handles
		// the binary fragment library.
		data, err := templates.ShowDB(name)
		if err == nil {
			return data, templates.ProvenanceHome, nil
		}
		if !errors.Is(err, templates.ErrDBNotFound) && !errors.Is(err, templates.ErrItemNotFound) {
			return nil, "", err
		}
		binData, binErr := templates.ShowItem(templates.Item{
			Kind: templates.KindSchema, Name: name, Provenance: templates.ProvenanceBinary,
		})
		if binErr == nil {
			return binData, templates.ProvenanceBinary, nil
		}
		// Surface the original ErrDBNotFound so existing tests'
		// "not found" diagnostics remain stable.
		return nil, "", err
	case "home":
		data, err := templates.ShowDB(name)
		if err != nil {
			return nil, "", err
		}
		return data, templates.ProvenanceHome, nil
	case "ta":
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindSchema, Name: name, Provenance: templates.ProvenanceBinary,
		})
		if err != nil {
			return nil, "", fmt.Errorf("show: schema %q not found in binary library: %w", name, err)
		}
		return data, templates.ProvenanceBinary, nil
	default:
		return nil, "", fmt.Errorf("unknown --provenance %q (want ta|home)", provFlag)
	}
}

func runTemplateShowItem(out io.Writer, name, kindFlag, group, provFlag string, asJSON bool) error {
	kind, err := parseShowKind(kindFlag)
	if err != nil {
		return err
	}
	provs := []templates.Provenance{templates.ProvenanceHome, templates.ProvenanceBinary}
	if provFlag != "" {
		switch provFlag {
		case "home":
			provs = []templates.Provenance{templates.ProvenanceHome}
		case "ta":
			provs = []templates.Provenance{templates.ProvenanceBinary}
		default:
			return fmt.Errorf("unknown --provenance %q (want ta|home)", provFlag)
		}
	}
	for _, p := range provs {
		data, err := templates.ShowItem(templates.Item{
			Kind: kind, Name: name, Group: group, Provenance: p,
		})
		if err == nil {
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"kind":       string(kind),
					"name":       name,
					"group":      group,
					"provenance": string(p),
					"bytes":      string(data),
				})
			}
			fmt.Fprint(out, string(data))
			if len(data) > 0 && data[len(data)-1] != '\n' {
				fmt.Fprintln(out)
			}
			return nil
		}
		if !errors.Is(err, templates.ErrItemNotFound) {
			return err
		}
	}
	return fmt.Errorf("show: %s %q (group=%q) not found in any provenance", kind, name, group)
}

func parseShowKind(s string) (templates.Kind, error) {
	switch s {
	case "schema":
		return templates.KindSchema, nil
	case "agent":
		return templates.KindAgent, nil
	case "config":
		return templates.KindConfig, nil
	case "docs-template":
		return templates.KindDocsTemplate, nil
	default:
		return "", fmt.Errorf("unknown --kind %q (want schema|agent|config|docs-template)", s)
	}
}

func renderTemplateBody(w io.Writer, name string, data []byte) error {
	body := string(data)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	wrapped := fmt.Sprintf("# `%s`\n\n```toml\n%s```\n", name, body)
	return render.New(w).Markdown(wrapped)
}

// ---- save ------------------------------------------------------------

// templateSaveReport is the --json emit shape for `ta template save`.
// Mirrors the SaveResult buckets so agents can distinguish written
// from skipped from conflicted dbs.
type templateSaveReport struct {
	Source    string   `json:"source"`
	Written   []string `json:"written"`
	Skipped   []string `json:"skipped"`
	Conflicts []string `json:"conflicts"`
}

func newTemplateSaveCmd() *cobra.Command {
	var overwrite bool
	var asJSON bool
	var kind string
	var substrate string
	var path string
	var group string
	var canonical string
	cmd := &cobra.Command{
		Use:   "save [<db>...]",
		Short: "Promote project content to ~/.ta (schema | agent | config | docs-template | substrate)",
		Long: "Promote project content into the home library. Default kind is " +
			"`schema`: reads `<cwd>/.ta/schema.toml`, validates it through the " +
			"meta-schema, and merges the named dbs into `~/.ta/schema.toml`. " +
			"`--kind=agent --path=<file> [--group=<subdir>]` copies an agent .md " +
			"file into `~/.ta/agents/<group>/<basename>` (creating `<group>` if " +
			"missing). `--kind=config --canonical=<name>` copies a project file " +
			"into `~/.ta/configs/<canonical>`; `--kind=docs-template " +
			"--canonical=<name>` does the same for `~/.ta/docs-templates/<canonical>.md`. " +
			"`--substrate=<name> --path=<file>` saves a file to the destination " +
			"derived from the named substrate (10 file-shaped defaults only; directory " +
			"bundles unsupported in this drop). Schema mode keeps the F15 conflict " +
			"semantics (TTY confirm prompt or `--overwrite`); other kinds error on " +
			"existing destinations unless `--overwrite` is set.",
		Example: `  ta template save
  ta template save plans
  ta template save plans notes
  ta template save --kind=agent --path=./.claude/agents/builder.md --group=go
  ta template save --kind=config --path=./.mcp.json --canonical=mcp.json
  ta template save --kind=docs-template --path=./CLAUDE.md --canonical=CLAUDE`,
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			// --kind and --substrate are mutually exclusive.
			if kind != "" && substrate != "" {
				return fmt.Errorf("save: --kind and --substrate are mutually exclusive (use one or the other)")
			}
			// If --substrate is specified, route to the substrate lane.
			if substrate != "" {
				return runTemplateSaveSubstrate(c.OutOrStdout(), substrate, path, group, canonical, overwrite, asJSON)
			}
			// Otherwise, legacy --kind branches.
			switch kind {
			case "", "schema":
				// Legacy warning is a stderr notice — emitted before the
				// merge so the user sees the legacy-files heads-up
				// alongside the save report. Mirrors the wire in
				// `template list` (this file, RunE above) and pins F15's
				// contract that save ALSO warns on legacy template
				// files, not just list.
				emitLegacyWarning(c.ErrOrStderr())
				return runTemplateSave(c.OutOrStdout(), args, overwrite, asJSON)
			case "agent":
				return runTemplateSaveAgent(c.OutOrStdout(), path, group, canonical, overwrite, asJSON)
			case "config":
				return runTemplateSaveFlat(c.OutOrStdout(), path, canonical, overwrite, asJSON, "config")
			case "docs-template":
				return runTemplateSaveFlat(c.OutOrStdout(), path, canonical, overwrite, asJSON, "docs-template")
			default:
				return fmt.Errorf("unknown --kind %q (want schema|agent|config|docs-template)", kind)
			}
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().StringVar(&kind, "kind", "", "what to save: schema (default) | agent | config | docs-template")
	cmd.Flags().StringVar(&substrate, "substrate", "", "substrate name (10 file-shaped defaults only; mutually exclusive with --kind)")
	cmd.Flags().StringVar(&path, "path", "", "source file path (required for kind=agent|config|docs-template or substrate)")
	cmd.Flags().StringVar(&group, "group", "", "agent subdir name under ~/.ta/agents/ (kind=agent or substrate=claude_agents only; empty = flat)")
	cmd.Flags().StringVar(&canonical, "canonical", "", "destination filename (kind=agent|config|docs-template or any substrate; defaults to basename of --path)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing destination without prompting")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered notice")
	return cmd
}

// runTemplateSaveAgent reads --path and copies it into
// `~/.ta/agents/<group>/<basename>`. Empty group → flat layout.
// When canonical is non-empty, it overrides the destination name
// (so `<path>=./.claude/agents/ta-go-builder.md --canonical=go-builder`
// writes `~/.ta/agents/<group>/go-builder.md`).
func runTemplateSaveAgent(out io.Writer, srcPath, group, canonical string, overwrite, asJSON bool) error {
	if srcPath == "" {
		return errors.New("save --kind=agent: --path is required")
	}
	body, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("save: read %s: %w", srcPath, err)
	}
	base := filepath.Base(srcPath)
	if !strings.HasSuffix(base, ".md") {
		return fmt.Errorf("save --kind=agent: source %q must end in .md", srcPath)
	}
	name := strings.TrimSuffix(base, ".md")
	if canonical != "" {
		name = strings.TrimSuffix(canonical, ".md")
	}
	if err := templates.SaveAgent(name, group, body, overwrite); err != nil {
		return err
	}
	report := templateSaveKindReport{
		Kind:    "agent",
		Source:  srcPath,
		Group:   group,
		Name:    name,
		Written: true,
	}
	return emitTemplateSaveKindReport(out, report, asJSON)
}

// runTemplateSaveFlat handles --kind=config and --kind=docs-template.
// canonical defaults to filepath.Base(path) when empty.
func runTemplateSaveFlat(out io.Writer, srcPath, canonical string, overwrite, asJSON bool, kind string) error {
	if srcPath == "" {
		return fmt.Errorf("save --kind=%s: --path is required", kind)
	}
	body, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("save: read %s: %w", srcPath, err)
	}
	if canonical == "" {
		canonical = filepath.Base(srcPath)
		if kind == "docs-template" {
			canonical = strings.TrimSuffix(canonical, ".md")
		}
	}
	switch kind {
	case "config":
		if err := templates.SaveConfig(canonical, body, overwrite); err != nil {
			return err
		}
	case "docs-template":
		if err := templates.SaveDocsTemplate(canonical, body, overwrite); err != nil {
			return err
		}
	}
	report := templateSaveKindReport{
		Kind:    kind,
		Source:  srcPath,
		Name:    canonical,
		Written: true,
	}
	return emitTemplateSaveKindReport(out, report, asJSON)
}

// runTemplateSaveSubstrate handles --substrate=<name>. It calls the D1 helper
// templates.SaveSubstrateFile and emits the result with uniform JSON output
// via the existing report shape.
func runTemplateSaveSubstrate(out io.Writer, substrate, srcPath, group, canonical string, overwrite, asJSON bool) error {
	if srcPath == "" {
		return fmt.Errorf("save --substrate=%s: --path is required", substrate)
	}
	// --group is only valid for grouped substrates (claude_agents).
	if group != "" && substrate != "claude_agents" {
		return fmt.Errorf("save: --group is only valid for --substrate=claude_agents; got %q", substrate)
	}
	// Default canonical to basename of --path when omitted (matches the
	// flag help text contract; without this default, SaveSubstrateFile's
	// destination path collapses to the parent dir and the conflict guard
	// fires a misleading "already exists" error on the dir itself).
	if canonical == "" {
		canonical = filepath.Base(srcPath)
	}
	// Call the D1 helper. Errors bubble up directly (bundle-unsupported,
	// unknown-substrate, file-not-found, and destination-exists errors are
	// all user-facing and come from the helper).
	if err := templates.SaveSubstrateFile(substrate, srcPath, group, canonical, overwrite); err != nil {
		return err
	}
	// Emit success report. For substrates, the "kind" field is prefixed with
	// "substrate:" and the "name" field holds the substrate name itself.
	report := templateSaveKindReport{
		Kind:    "substrate:" + substrate,
		Source:  srcPath,
		Group:   group,
		Name:    substrate,
		Written: true,
	}
	return emitTemplateSaveKindReport(out, report, asJSON)
}

// templateSaveKindReport is the JSON shape for non-schema save kinds.
type templateSaveKindReport struct {
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Group   string `json:"group,omitempty"`
	Name    string `json:"name"`
	Written bool   `json:"written"`
}

func emitTemplateSaveKindReport(w io.Writer, r templateSaveKindReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	headline := r.Kind + ":" + r.Name
	if r.Group != "" {
		headline = r.Kind + ":" + r.Group + "/" + r.Name
	}
	detail := []string{"source: " + r.Source}
	return render.New(w).Success("ta template save", headline, detail)
}

// runTemplateSave orchestrates the save flow: read project schema,
// pre-validate, do a dry-run merge to surface conflicts, prompt or
// auto-decide based on TTY + flags, then call SaveDBs with the
// resolved Overwrite decision and emit the report.
func runTemplateSave(out io.Writer, names []string, overwrite, asJSON bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}
	sourcePath := filepath.Join(cwd, ".ta", "schema.toml")
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("save: %s does not exist; run `ta init` first", sourcePath)
		}
		return fmt.Errorf("read %s: %w", sourcePath, err)
	}
	// Pre-validate so a malformed project schema errors with a
	// line/column pointing at sourcePath BEFORE templates.SaveDBs
	// runs (which would surface the same error wrapped at the home
	// path).
	if _, err := schema.LoadBytes(data); err != nil {
		return fmt.Errorf("save: validate %s: %w", sourcePath, err)
	}

	// Validate every requested name eagerly so the user sees a
	// per-name validation error (with name context) rather than a
	// generic "templates: invalid db name: ..." from the second pass
	// inside SaveDBs.
	for _, n := range names {
		if err := validateDBNameForCLI(n); err != nil {
			return err
		}
	}

	// Conflict-prompt path: detect conflicts BEFORE the real merge so
	// we can prompt once (TTY) or auto-skip (off-TTY). Avoids calling
	// SaveDBs twice (which would write on the first call when there
	// are zero conflicts and then see all dbs as conflicts on the
	// second). Conflicts are simply (project dbs ∩ home dbs).
	nonInteractive := overwrite || asJSON
	resolveOverwrite := overwrite
	if !overwrite {
		conflicts, err := detectSaveConflicts(data, names)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 && ttyInteractive(nonInteractive) {
			ok, err := promptSaveConflicts(conflicts)
			if err != nil {
				return err
			}
			if ok {
				resolveOverwrite = true
			}
		}
	}

	res, err := templates.SaveDBs(data, names, templates.SaveOptions{Overwrite: resolveOverwrite})
	if err != nil {
		return err
	}

	report := templateSaveReport{
		Source:    sourcePath,
		Written:   res.Written,
		Skipped:   res.Skipped,
		Conflicts: res.Conflicts,
	}
	return emitTemplateSaveReport(out, report, asJSON)
}

// validateDBNameForCLI front-loads name validation with a
// CLI-friendly error wrapper. SaveDBs would otherwise emit the same
// rejection but the pre-validation makes the diagnostic appear before
// any other work.
func validateDBNameForCLI(name string) error {
	if name == "" {
		return errors.New("save: empty db name")
	}
	return nil
}

// detectSaveConflicts returns the sorted set of db names that would
// collide between the project schema and the current home schema,
// scoped to the (possibly empty) names filter. Used by runTemplateSave
// to size the confirm prompt without performing a write. Loading the
// project Registry costs one parse on bytes already validated upstream
// — cheap relative to the round-trip risk of double-calling SaveDBs.
func detectSaveConflicts(projectBytes []byte, names []string) ([]string, error) {
	projectReg, err := schema.LoadBytes(projectBytes)
	if err != nil {
		return nil, fmt.Errorf("save: validate project schema: %w", err)
	}
	homeReg, _, err := templates.LoadHome()
	if err != nil {
		return nil, err
	}
	if len(homeReg.DBs) == 0 {
		return nil, nil
	}
	wanted := make(map[string]struct{})
	if len(names) == 0 {
		for n := range projectReg.DBs {
			wanted[n] = struct{}{}
		}
	} else {
		for _, n := range names {
			if _, ok := projectReg.DBs[n]; ok {
				wanted[n] = struct{}{}
			}
		}
	}
	var conflicts []string
	for n := range wanted {
		if _, dup := homeReg.DBs[n]; dup {
			conflicts = append(conflicts, n)
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

// promptSaveConflicts asks (once, sized to the conflict count)
// whether to overwrite every conflicting home db. Per F15 design
// decision #3 + plan §3: a single yes/no prompt scales better than
// per-db prompts when the user already filtered via positional args.
func promptSaveConflicts(conflicts []string) (bool, error) {
	title := fmt.Sprintf("Overwrite %d existing db(s) in ~/.ta/schema.toml? [%s]",
		len(conflicts), strings.Join(conflicts, ", "))
	return runConfirmProgram(title, "Overwrite", "Skip", false)
}

func emitTemplateSaveReport(w io.Writer, r templateSaveReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	detail := []string{
		fmt.Sprintf("source: %s", r.Source),
	}
	if len(r.Written) > 0 {
		detail = append(detail, "written: "+strings.Join(r.Written, ", "))
	}
	if len(r.Skipped) > 0 {
		detail = append(detail, "skipped: "+strings.Join(r.Skipped, ", "))
	}
	if len(r.Conflicts) > 0 {
		detail = append(detail, "conflicts: "+strings.Join(r.Conflicts, ", "))
	}
	headline := strings.Join(r.Written, ", ")
	if headline == "" {
		headline = "(none written)"
	}
	return render.New(w).Success("ta template save", headline, detail)
}

// ---- apply -----------------------------------------------------------

// templateApplyReport is the --json emit shape for `ta template apply`.
// Apply extracts one db from `~/.ta/schema.toml` and writes a
// project-side schema containing just that db.
type templateApplyReport struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	Written bool   `json:"written"`
}

func newTemplateApplyCmd() *cobra.Command {
	var force bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "apply <db>",
		Short: "Extract one db from ~/.ta/schema.toml into <path>/.ta/schema.toml",
		Long: "Reads the named db from `~/.ta/schema.toml`, marshals a project " +
			"schema containing just that db, and writes it to " +
			"`<path>/.ta/schema.toml`. --path defaults to cwd; relative or " +
			"absolute accepted. Creates the `.ta/` directory if missing. " +
			"If the target already exists, confirms on a TTY or " +
			"requires `--force` off-TTY. Schema-only — does NOT touch " +
			"`.mcp.json` / `.codex/config.toml` (use `ta init` for a full " +
			"bootstrap).",
		Example: `  ta template apply plans
  ta template apply plans --path /abs/path/proj
  ta template apply plans --path /abs/path --force --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			target, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			return runTemplateApply(c.OutOrStdout(), name, target, force, asJSON)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing <path>/.ta/schema.toml without prompting")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered notice")
	addPathFlag(cmd)
	return cmd
}

func runTemplateApply(out io.Writer, name, target string, force, asJSON bool) error {
	data, err := templates.ShowDB(name)
	if err != nil {
		return err
	}

	destDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", destDir, err)
	}
	destPath := filepath.Join(destDir, "schema.toml")

	nonInteractive := force || asJSON
	if _, err := os.Stat(destPath); err == nil {
		switch {
		case force:
			// fall through to overwrite
		case ttyInteractive(nonInteractive):
			ok, err := promptConfirm(fmt.Sprintf("Overwrite existing %s?", destPath))
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("apply: %s exists; aborted (pass --force to overwrite without prompt)", destPath)
			}
		default:
			return fmt.Errorf("apply: %s exists; pass --force to overwrite", destPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", destPath, err)
	}

	if err := fsatomic.Write(destPath, data); err != nil {
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	report := templateApplyReport{Name: name, Target: destPath, Written: true}
	return emitTemplateApplyReport(out, report, asJSON)
}

func emitTemplateApplyReport(w io.Writer, r templateApplyReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	detail := []string{
		fmt.Sprintf("target: %s", r.Target),
	}
	return render.New(w).Success("ta template apply", r.Name, detail)
}

// ---- delete ----------------------------------------------------------

// templateDeleteReport is the --json emit shape for `ta template delete`.
type templateDeleteReport struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

func newTemplateDeleteCmd() *cobra.Command {
	var force bool
	var asJSON bool
	var kind string
	var group string
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Remove one item from the home template library",
		Long: "Removes one home-library item. Default `--kind=schema` removes the " +
			"named db from `~/.ta/schema.toml`. `--kind=agent --group=<group>` " +
			"removes one home agent. `--kind=config` / `--kind=docs-template` " +
			"remove one home flat-library item. Binary items are read-only and " +
			"refuse to delete (ErrReadOnly). Confirms on a TTY; requires " +
			"`--force` off-TTY.",
		Example: `  ta template delete old-plans
  ta template delete old-plans --force
  ta template delete go-builder --kind=agent --group=go --force
  ta template delete CLAUDE --kind=docs-template --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if kind == "" || kind == "schema" {
				return runTemplateDelete(c.OutOrStdout(), args[0], force, asJSON)
			}
			return runTemplateDeleteItem(c.OutOrStdout(), args[0], kind, group, force, asJSON)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip the confirm prompt")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered notice")
	cmd.Flags().StringVar(&kind, "kind", "", "item kind: schema (default) | agent | config | docs-template")
	cmd.Flags().StringVar(&group, "group", "", "agent subdir (kind=agent only)")
	return cmd
}

func runTemplateDeleteItem(out io.Writer, name, kindFlag, group string, force, asJSON bool) error {
	nonInteractive := force || asJSON
	switch {
	case force:
		// fall through
	case ttyInteractive(nonInteractive):
		ok, err := promptConfirm(fmt.Sprintf("Delete home %s %q?", kindFlag, name))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("delete: aborted; %s %q left in place", kindFlag, name)
		}
	default:
		return fmt.Errorf("delete: %s %q requires --force off a TTY", kindFlag, name)
	}

	switch kindFlag {
	case "agent":
		if err := templates.DeleteAgent(name, group); err != nil {
			return err
		}
	case "config":
		if err := templates.DeleteConfig(name); err != nil {
			return err
		}
	case "docs-template":
		if err := templates.DeleteDocsTemplate(name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown --kind %q (want schema|agent|config|docs-template)", kindFlag)
	}
	report := templateDeleteKindReport{
		Kind:    kindFlag,
		Group:   group,
		Name:    name,
		Deleted: true,
	}
	return emitTemplateDeleteKindReport(out, report, asJSON)
}

type templateDeleteKindReport struct {
	Kind    string `json:"kind"`
	Group   string `json:"group,omitempty"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

func emitTemplateDeleteKindReport(w io.Writer, r templateDeleteKindReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	headline := r.Kind + ":" + r.Name
	if r.Group != "" {
		headline = r.Kind + ":" + r.Group + "/" + r.Name
	}
	return render.New(w).Success("ta template delete", headline, nil)
}

func runTemplateDelete(out io.Writer, name string, force, asJSON bool) error {
	// Existence check up front so the "missing" error is loud and
	// crisp before any prompting. Using ShowDB is a cheap probe — it
	// validates the name and surfaces ErrDBNotFound.
	if _, err := templates.ShowDB(name); err != nil {
		if errors.Is(err, templates.ErrDBNotFound) {
			return fmt.Errorf("delete: db %q not found in ~/.ta/schema.toml", name)
		}
		return err
	}

	nonInteractive := force || asJSON
	switch {
	case force:
		// fall through to delete
	case ttyInteractive(nonInteractive):
		ok, err := promptConfirm(fmt.Sprintf("Delete db %q from ~/.ta/schema.toml?", name))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("delete: aborted; db %q left in place", name)
		}
	default:
		return fmt.Errorf("delete: db %q requires --force off a TTY", name)
	}

	if err := templates.DeleteDB(name); err != nil {
		return err
	}
	report := templateDeleteReport{Name: name, Deleted: true}
	return emitTemplateDeleteReport(out, report, asJSON)
}

func emitTemplateDeleteReport(w io.Writer, r templateDeleteReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	return render.New(w).Success("ta template delete", r.Name, nil)
}

// ---- shared confirm helpers -----------------------------------------

// promptConfirm is the shared bubbletea confirm used by apply /
// delete flows (init has its own confirmOverwrite for title
// phrasing).
func promptConfirm(title string) (bool, error) {
	return runConfirmProgram(title, "Yes", "No", false)
}
