package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"
	"github.com/evanmschultz/laslig"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/fsatomic"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/schema"
	"github.com/evanmschultz/ta/internal/templates"
)

// metaFieldDescription is the db-level `description` key on a schema
// table. Mirrors the unexported `schema.metaFieldDescription` constant
// — tiny stable string, not worth widening the schema package surface
// just to avoid the duplicate literal.
const metaFieldDescription = "description"

// emptyProjectSchemaHeader is the comment-only body written to
// `<project>/.ta/schema.toml` when the user selects zero dbs from the
// Phase 9.5 db-multi-select. The cascade resolver tolerates a registry
// with no dbs (Lookup returns ok=false; nothing routes there); the
// remediation hints in the comment guide the user toward
// `ta schema --action=create` or hand-editing.
const emptyProjectSchemaHeader = "# Project schema — no dbs declared yet.\n" +
	"# Run `ta schema --action=create --kind=db --name=<name> --data='{...}'`\n" +
	"# to declare a db, or copy from examples/ in the ta repo.\n"

// claudeMCPFileName is the canonical `.mcp.json` filename Claude Code
// reads from the project root (V2-PLAN §14.4).
const claudeMCPFileName = ".mcp.json"

// codexMCPDir / codexMCPFile is the canonical `.codex/config.toml`
// location Codex reads from the project root (V2-PLAN §14.4).
const (
	codexMCPDir  = ".codex"
	codexMCPFile = "config.toml"
)

// initFlags is the parsed `ta init` flag set, collected in one struct
// so the bootstrap logic does not need seven positional arguments.
type initFlags struct {
	template   string
	noClaude   bool
	noCodex    bool
	force      bool
	asJSON     bool
	nonInterRq bool // non-interactive because of flags, not TTY absence
}

// initReport is the structured outcome of `ta init`. The --json emit
// shape mirrors it verbatim (V2-PLAN §14 bootstrap contract).
//
// SchemaSource takes one of three shapes per PLAN §12.17.9 Phase 9.5:
//
//   - "<template-name>" — the user passed `--template <name>` (or an
//     off-TTY `bootstrap.default_template`). The full file was copied.
//   - "dbs:<sorted-csv>" — interactive picker; the listed dbs were
//     reconstructed into the project schema.
//   - "(empty)" — interactive picker with zero dbs selected; a
//     comment-only schema was written.
type initReport struct {
	Path          string `json:"path"`
	SchemaSource  string `json:"schema_source"`
	ClaudeWritten bool   `json:"claude_written"`
	CodexWritten  bool   `json:"codex_written"`
}

// bootstrapConfig is the optional `<path>/.ta/config.toml` shape.
// See V2-PLAN §14.5. CLI flags override these keys.
type bootstrapConfig struct {
	Bootstrap struct {
		Claude          *bool  `toml:"claude"`
		Codex           *bool  `toml:"codex"`
		DefaultTemplate string `toml:"default_template"`
	} `toml:"bootstrap"`
}

func newInitCmd() *cobra.Command {
	f := initFlags{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a project directory with a schema and MCP configs",
		Long: "Bootstrap a project directory from the `~/.ta/schema.toml` " +
			"template library. With a TTY and no flags, runs an interactive " +
			"huh multi-select over every db declared in the home library; " +
			"the chosen dbs are reconstructed into `<path>/.ta/schema.toml`. " +
			"Selecting zero dbs writes a comment-only schema you can fill " +
			"in later via `ta schema --action=create`. With " +
			"`--template <db>`, exactly that one db is extracted from the " +
			"home schema (post-F15 the flag selects a db-name, not a " +
			"per-template filename). By default also writes " +
			"`<path>/.mcp.json` (Claude Code) and " +
			"`<path>/.codex/config.toml` (Codex). Per-path " +
			"defaults can be set in `<path>/.ta/config.toml` (V2-PLAN §14.5); " +
			"`ta init` does NOT create that file itself — edit it by hand to " +
			"tune future `ta init` runs on the same path. --path defaults to " +
			"cwd; relative or absolute accepted (V2-PLAN §12.17.5 [A1]).",
		Example: "  ta init\n  ta init --path /abs/path/to/new-project --template plans\n  ta init --path /abs/path --template plans --no-codex --json",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			target, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			// --json is treated as a non-interactive request: agents
			// piping stdout expect structured JSON and cannot complete
			// a huh form. Without this, `ta init --json` from a TTY
			// would block on the picker then emit JSON afterward (QA
			// falsification §12.14 LOW-2 finding).
			f.nonInterRq = f.template != "" || f.asJSON
			return runInit(c.OutOrStdout(), c.ErrOrStderr(), c.InOrStdin(), target, f)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().StringVar(&f.template, "template", "", "name of one db in ~/.ta/schema.toml (skips huh picker)")
	cmd.Flags().BoolVar(&f.noClaude, "no-claude", false, "skip .mcp.json generation")
	cmd.Flags().BoolVar(&f.noCodex, "no-codex", false, "skip .codex/config.toml generation")
	cmd.Flags().BoolVar(&f.force, "force", false, "overwrite an existing .ta/schema.toml without prompting")
	cmd.Flags().BoolVar(&f.asJSON, "json", false, "emit JSON instead of laslig-rendered notices")
	addPathFlag(cmd)
	return cmd
}

// runInit orchestrates bootstrap: mkdir -p the target, resolve
// template choice (flag or picker), validate schema write (force /
// confirm path), write schema, then write the two MCP configs honoring
// flags and `<path>/.ta/config.toml`. `errOut` receives diagnostic
// warnings (e.g. skipped malformed templates) so they never pollute
// stdout — agents reading `--json` output on stdout see no warning
// prefix.
func runInit(out, errOut io.Writer, in io.Reader, target string, f initFlags) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}

	bootCfg, err := readBootstrapConfig(target)
	if err != nil {
		return err
	}

	effClaude, effCodex := effectiveMCPToggles(f, bootCfg)

	schemaSource, schemaBytes, err := chooseSchema(in, out, errOut, f, bootCfg)
	if err != nil {
		return err
	}

	schemaPath := filepath.Join(target, ".ta", "schema.toml")
	if err := writeSchema(in, out, schemaPath, schemaBytes, f); err != nil {
		return err
	}

	// If the picker / non-interactive flow did not lock in claude/codex
	// toggles, the effective-toggles layer already picked defaults. On
	// an interactive TTY with no explicit toggles, ask through huh.
	if interactive(in, out, f) && !bootCfgHasMCPKeys(bootCfg) && !f.noClaude && !f.noCodex {
		c, x, err := promptMCPToggles(effClaude, effCodex)
		if err != nil {
			return err
		}
		effClaude, effCodex = c, x
	}

	claudeWritten, err := maybeWriteClaudeMCP(target, effClaude)
	if err != nil {
		return err
	}
	codexWritten, err := maybeWriteCodexMCP(target, effCodex)
	if err != nil {
		return err
	}

	report := initReport{
		Path:          target,
		SchemaSource:  schemaSource,
		ClaudeWritten: claudeWritten,
		CodexWritten:  codexWritten,
	}
	return emitInitReport(out, report, f.asJSON)
}

// chooseSchema resolves which schema bytes to write. Three paths:
//
//  1. Explicit `--template <db>`: extract that one db from
//     `~/.ta/schema.toml` and emit a project schema containing just
//     that db. F15 reinterpreted the flag from "filename" to
//     "db-name"; pre-F15 callers passing a multi-template filename
//     break loudly via templates.ErrDBNotFound. Source label = "<db>".
//  2. Off-TTY with `bootstrap.default_template`: same single-db
//     extract using the configured db name. Source label = "<db>".
//  3. TTY interactive (no `--template` and no off-TTY default): the
//     picker is a multi-select over every db declared in
//     `~/.ta/schema.toml`; the project schema is reconstructed from
//     the selected subset. Source label = "dbs:<csv>" or "(empty)".
//
// `errOut` receives a one-line laslig warning if any legacy
// `~/.ta/<name>.toml` files remain alongside `schema.toml` (pre-F15
// leftovers, ignored per F15 design decision).
//
// Per V2-PLAN §12.17.5 [D2]: a home with zero dbs raises a
// laslig-structured "home library is empty" notice pointing at
// `examples/` + the new `ta template save` flow.
func chooseSchema(in io.Reader, out, errOut io.Writer, f initFlags, cfg bootstrapConfig) (string, []byte, error) {
	emitInitLegacyWarning(errOut)

	if f.template != "" {
		body, err := loadHomeDBSubset(f.template)
		if err != nil {
			return "", nil, err
		}
		return f.template, body, nil
	}

	// Load home once. Missing file or zero-db registry both raise the
	// empty-home guidance — picking from an empty picker (or silently
	// falling through to the non-interactive error) is worse UX than
	// naming the problem.
	reg, raw, err := templates.LoadHome()
	if err != nil {
		return "", nil, err
	}
	if len(reg.DBs) == 0 {
		root, _ := templates.Root()
		return "", nil, emptyHomeError(errOut, root)
	}

	// No explicit flag. On a TTY run the picker; off-TTY honor
	// bootstrap default if set, else error loudly (non-interactive
	// with no db selection is ambiguous).
	if !interactive(in, out, f) {
		if cfg.Bootstrap.DefaultTemplate != "" {
			body, err := loadHomeDBSubset(cfg.Bootstrap.DefaultTemplate)
			if err != nil {
				return "", nil, err
			}
			return cfg.Bootstrap.DefaultTemplate, body, nil
		}
		return "", nil, errors.New("init: no db selected. Populate ~/.ta/schema.toml first (see examples/ in the ta repo, or run `ta template save` from a project), or run on a TTY for the picker.")
	}

	bodies, infos, err := homeDBPickerInputs(raw)
	if err != nil {
		return "", nil, err
	}

	selected, err := pickDBs(infos)
	if err != nil {
		return "", nil, err
	}

	body, err := buildProjectSchemaBytes(bodies, selected)
	if err != nil {
		return "", nil, err
	}
	return schemaSourceLabel(selected), body, nil
}

// emitInitLegacyWarning surfaces a one-line laslig warning when
// `~/.ta/` carries legacy per-template files alongside schema.toml.
// Same shape as cmd/ta/template_cmd.go's emitLegacyWarning — kept
// separate so init does not depend on a template-cmd helper. Per F15
// design decision #4: warn-only, no auto-migration.
func emitInitLegacyWarning(errOut io.Writer) {
	files, err := templates.LegacyTemplateFiles()
	if err != nil || len(files) == 0 {
		return
	}
	root, _ := templates.Root()
	bases := make([]string, 0, len(files))
	for _, p := range files {
		bases = append(bases, filepath.Base(p))
	}
	_ = render.New(errOut).Notice(
		laslig.NoticeWarningLevel,
		"legacy template files detected",
		fmt.Sprintf("ignoring %d legacy template file(s) in %s; merge into schema.toml or remove",
			len(files), root),
		bases,
	)
}

// loadHomeDBSubset extracts one db's body from `~/.ta/schema.toml`
// and emits project-schema bytes containing just that db. Returns
// templates.ErrDBNotFound when the named db is absent, including the
// "F15 loud break" case where a pre-F15 caller passed a filename
// instead of a db-name.
func loadHomeDBSubset(name string) ([]byte, error) {
	return templates.ShowDB(name)
}

// homeDBPickerInputs decodes the home schema bytes into the {dbName →
// body} map and the picker rows the multi-select consumes. Replaces
// the pre-F15 cross-template merge: dbs are now uniquely keyed by name
// in one file, no first-wins shadow needed.
func homeDBPickerInputs(raw []byte) (map[string]map[string]any, []dbPickerInfo, error) {
	var rawMap map[string]any
	if err := toml.Unmarshal(raw, &rawMap); err != nil {
		return nil, nil, fmt.Errorf("init: parse home schema: %w", err)
	}
	bodies := make(map[string]map[string]any, len(rawMap))
	for k, v := range rawMap {
		body, ok := v.(map[string]any)
		if !ok {
			continue
		}
		bodies[k] = body
	}

	infos := make([]dbPickerInfo, 0, len(bodies))
	for name, body := range bodies {
		display := name
		if d, ok := body[metaFieldDescription].(string); ok {
			d = strings.TrimSpace(d)
			if d != "" {
				display = name + " — " + d
			}
		}
		infos = append(infos, dbPickerInfo{name: name, displayName: display})
	}
	slices.SortFunc(infos, func(a, b dbPickerInfo) int {
		return strings.Compare(a.name, b.name)
	})
	return bodies, infos, nil
}

// emptyHomeError emits a laslig-structured "home library is empty"
// notice to errOut and returns a Go error carrying the same
// remediation pointers so non-laslig surfaces — fang's error printer,
// piped stderr, test buffers — still expose the path forward. Per
// V2-PLAN §12.17.5 [D2] (2026-04-24 amendment); F15 updated the
// guidance from "examples/ + mage install" to "examples/ +
// `ta template save` from a project" since `mage install` now copies
// a delivery binary, not a home schema.
func emptyHomeError(errOut io.Writer, root string) error {
	rr := render.New(errOut)
	schemaPath := filepath.Join(root, "schema.toml")
	_ = rr.Notice(
		laslig.NoticeErrorLevel,
		"home library is empty",
		fmt.Sprintf("ta init needs at least one schema source but %s has no "+
			"declared dbs. Sample schemas live in the ta repo under "+
			"examples/ — copy one in, build a schema with the CLI and "+
			"promote it via `ta template save`, or hand-edit "+
			"`%s`.", root, schemaPath),
		[]string{
			"Copy a sample: cp examples/schema.toml " + schemaPath,
			"Or hand-edit: $EDITOR " + schemaPath,
			"Or build via CLI: ta schema --action=create --kind=db --name=<name> --data='{...}'",
			"Or promote from a project: ta template save (after building schema in a project)",
			"Sample schemas live in the ta repo under examples/",
		},
	)
	return fmt.Errorf("init: home library is empty at %s; see examples/ in the ta repo", root)
}

// dbPickerInfo carries one row of the multi-select option list: the
// db's name (the bound value), and a one-line display label combining
// the db name with a short description excerpt when present.
type dbPickerInfo struct {
	name        string
	displayName string // "<dbname> — <description>" or just "<dbname>"
}

// pickDBs runs the huh multi-select over the home-library db catalogue.
// Returns the selected db names in the order huh wrote them (huh
// preserves option-list order in the bound slice). Selecting zero is a
// valid outcome — the empty-schema branch handles it downstream.
func pickDBs(infos []dbPickerInfo) ([]string, error) {
	opts := make([]huh.Option[string], 0, len(infos))
	for _, i := range infos {
		opts = append(opts, huh.NewOption(i.displayName, i.name))
	}
	var selected []string
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Pick dbs to include in this project (space to toggle, enter to confirm)").
			Description("Selecting zero is fine — you can declare dbs later via `ta schema --action=create`.").
			Options(opts...).
			Value(&selected),
	))
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("db picker: %w", err)
	}
	return selected, nil
}

// buildProjectSchemaBytes returns the bytes to write to
// `<project>/.ta/schema.toml` given the home-library db bodies and the
// user's selection. Zero selection writes the comment-only header;
// any other selection marshals the selected subset and re-validates
// the round-trip via schema.LoadBytes so a bad selection (e.g.
// overlapping `paths` across two selected dbs) surfaces here rather
// than at next read.
func buildProjectSchemaBytes(bodies map[string]map[string]any, selected []string) ([]byte, error) {
	if len(selected) == 0 {
		return []byte(emptyProjectSchemaHeader), nil
	}
	return subsetSchema(bodies, selected)
}

// subsetSchema marshals the named subset of bodies into TOML bytes and
// re-validates the result via schema.LoadBytes. Iterates selected in
// sorted order so repeat runs over the same selection produce
// byte-identical output (pelletier/go-toml/v2 marshals map[string]any
// keys in their natural map-iteration order, but a sorted intermediate
// `map[string]any` would still be re-iterated by go-toml in
// sorted-string order — sorting up front keeps the trace explicit).
func subsetSchema(bodies map[string]map[string]any, selected []string) ([]byte, error) {
	subset := make(map[string]any, len(selected))
	sorted := append([]string(nil), selected...)
	slices.Sort(sorted)
	for _, name := range sorted {
		body, ok := bodies[name]
		if !ok {
			return nil, fmt.Errorf("init: db %q missing from home library", name)
		}
		subset[name] = body
	}
	buf, err := toml.Marshal(subset)
	if err != nil {
		return nil, fmt.Errorf("init: marshal subset schema: %w", err)
	}
	if _, err := schema.LoadBytes(buf); err != nil {
		return nil, fmt.Errorf("init: subset schema invalid (overlap or meta-schema violation): %w", err)
	}
	return buf, nil
}

// schemaSourceLabel returns the SchemaSource value for the init report
// when the user took the multi-select path. "(empty)" for zero
// selection, "dbs:<csv>" for one or more (sorted for determinism).
func schemaSourceLabel(selected []string) string {
	if len(selected) == 0 {
		return "(empty)"
	}
	sorted := append([]string(nil), selected...)
	slices.Sort(sorted)
	return "dbs:" + strings.Join(sorted, ",")
}

// writeSchema handles the .ta/schema.toml write: directory creation,
// existence + --force + huh-confirm flow, then atomic write.
func writeSchema(in io.Reader, out io.Writer, schemaPath string, data []byte, f initFlags) error {
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		return fmt.Errorf("create .ta dir: %w", err)
	}
	if _, err := os.Stat(schemaPath); err == nil {
		if f.force {
			// fall through to overwrite
		} else if interactive(in, out, f) {
			ok, err := confirmOverwrite(schemaPath)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("init: %s exists; aborted (pass --force to overwrite without prompt)", schemaPath)
			}
		} else {
			return fmt.Errorf("init: %s exists; pass --force to overwrite", schemaPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", schemaPath, err)
	}
	if err := fsatomic.Write(schemaPath, data); err != nil {
		return fmt.Errorf("write schema: %w", err)
	}
	return nil
}

func confirmOverwrite(path string) (bool, error) {
	var ok bool
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Overwrite existing %s?", path)).
			Value(&ok),
	))
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("confirm prompt: %w", err)
	}
	return ok, nil
}

// promptMCPToggles offers the two MCP-target toggles via a single
// multi-select. Both default to selected per V2-PLAN §14.4 / §14.5.
func promptMCPToggles(claude, codex bool) (bool, bool, error) {
	const (
		optClaude = "Claude Code (.mcp.json)"
		optCodex  = "Codex (.codex/config.toml)"
	)
	selected := make([]string, 0, 2)
	if claude {
		selected = append(selected, optClaude)
	}
	if codex {
		selected = append(selected, optCodex)
	}
	opts := []huh.Option[string]{
		huh.NewOption(optClaude, optClaude).Selected(claude),
		huh.NewOption(optCodex, optCodex).Selected(codex),
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Generate MCP client configs?").
			Options(opts...).
			Value(&selected),
	))
	if err := form.Run(); err != nil {
		return false, false, fmt.Errorf("mcp prompt: %w", err)
	}
	outClaude, outCodex := false, false
	for _, s := range selected {
		switch s {
		case optClaude:
			outClaude = true
		case optCodex:
			outCodex = true
		}
	}
	return outClaude, outCodex, nil
}

// readBootstrapConfig reads `<target>/.ta/config.toml` if present.
// Absent file returns a zero-value config (not an error).
func readBootstrapConfig(target string) (bootstrapConfig, error) {
	p := filepath.Join(target, ".ta", "config.toml")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return bootstrapConfig{}, nil
		}
		return bootstrapConfig{}, fmt.Errorf("read %s: %w", p, err)
	}
	var cfg bootstrapConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return bootstrapConfig{}, fmt.Errorf("parse %s: %w", p, err)
	}
	return cfg, nil
}

func bootCfgHasMCPKeys(c bootstrapConfig) bool {
	return c.Bootstrap.Claude != nil || c.Bootstrap.Codex != nil
}

// effectiveMCPToggles merges CLI flags, bootstrap config, and defaults
// per V2-PLAN §14.5. CLI flags are the strongest override; bootstrap
// config is the next layer; defaults are `true` for both targets.
func effectiveMCPToggles(f initFlags, cfg bootstrapConfig) (claude, codex bool) {
	claude, codex = true, true
	if cfg.Bootstrap.Claude != nil {
		claude = *cfg.Bootstrap.Claude
	}
	if cfg.Bootstrap.Codex != nil {
		codex = *cfg.Bootstrap.Codex
	}
	if f.noClaude {
		claude = false
	}
	if f.noCodex {
		codex = false
	}
	return claude, codex
}

// interactive returns true when the caller is attached to a TTY AND
// no non-interactive flag (--template / --json) was set, so the
// picker is both possible and wanted.
func interactive(_ io.Reader, _ io.Writer, f initFlags) bool {
	return ttyInteractive(f.nonInterRq)
}

// ttyInteractive is the shared TTY-vs-flags gate used by `ta init` and
// every `ta template *` write subcommand. Returns true only when both
// stdin AND stdout are TTYs AND the caller has NOT forced a
// non-interactive path via flags (e.g. `--force`, `--json`,
// `--template`). Matching `os.Stdin` / `os.Stdout` keeps
// behavior consistent across commands: cobra's per-cmd io.Reader /
// io.Writer are test buffers that cannot report TTY-ness, so the
// process-level descriptors are the real signal.
func ttyInteractive(nonInteractive bool) bool {
	if nonInteractive {
		return false
	}
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// maybeWriteClaudeMCP writes or merges `<target>/.mcp.json`. Existing
// files with a pre-existing `ta` entry are left untouched per V2-PLAN
// §14.9 last bullet.
func maybeWriteClaudeMCP(target string, enabled bool) (bool, error) {
	if !enabled {
		return false, nil
	}
	path := filepath.Join(target, claudeMCPFileName)
	merged, changed, err := mergeClaudeMCP(path)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := fsatomic.Write(path, merged); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// mergeClaudeMCP returns the desired bytes for `.mcp.json` and a flag
// indicating whether the file on disk must change. Pre-existing files
// with a `ta` entry return (nil, false, nil).
func mergeClaudeMCP(path string) ([]byte, bool, error) {
	canonical := map[string]any{
		"command": "ta",
		"args":    []string{},
		"env":     map[string]string{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			payload := map[string]any{
				"mcpServers": map[string]any{"ta": canonical},
			}
			out, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return nil, false, err
			}
			out = append(out, '\n')
			return out, true, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	serversAny, ok := doc["mcpServers"]
	if !ok {
		doc["mcpServers"] = map[string]any{"ta": canonical}
	} else {
		servers, ok := serversAny.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: mcpServers must be a JSON object", path)
		}
		if _, exists := servers["ta"]; exists {
			return nil, false, nil
		}
		servers["ta"] = canonical
		doc["mcpServers"] = servers
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, err
	}
	out = append(out, '\n')
	return out, true, nil
}

// maybeWriteCodexMCP writes or merges `<target>/.codex/config.toml`.
// A pre-existing `[mcp_servers.ta]` table is left untouched.
func maybeWriteCodexMCP(target string, enabled bool) (bool, error) {
	if !enabled {
		return false, nil
	}
	dir := filepath.Join(target, codexMCPDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, codexMCPFile)
	merged, changed, err := mergeCodexMCP(path)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := fsatomic.Write(path, merged); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// canonicalCodexBlock is the exact `[mcp_servers.ta]` table we emit
// per V2-PLAN §14.4. Kept as a literal so generation stays byte-stable
// for MCP client compatibility.
const canonicalCodexBlock = "[mcp_servers.ta]\ncommand = \"ta\"\nargs = []\n"

// mergeCodexMCP returns the desired `.codex/config.toml` bytes and a
// changed flag. Missing file: write the canonical block. Existing file
// containing `[mcp_servers.ta]`: leave untouched. Existing file missing
// the block: append the canonical block verbatim (string-level append)
// so pre-existing `[mcp_servers.*]` tables survive byte-identically,
// avoiding go-toml round-trip reformatting.
func mergeCodexMCP(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte(canonicalCodexBlock), true, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if containsTable(string(data), "mcp_servers.ta") {
		return nil, false, nil
	}
	// Ensure a trailing newline before the appended block so the
	// resulting TOML parses as expected.
	body := string(data)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	// Separate from any preceding content with a blank line for
	// readability; harmless per TOML grammar.
	if !strings.HasSuffix(body, "\n\n") {
		body += "\n"
	}
	body += canonicalCodexBlock
	return []byte(body), true, nil
}

// containsTable checks whether a TOML document already declares the
// given dotted table header. Walks lines because round-tripping
// through go-toml would reformat the user's file.
//
// Matches TOML-equivalent whitespace variants per v1.0.0 grammar:
// `[ mcp_servers.ta ]`, `[mcp_servers . ta]`, `[mcp_servers."ta"]`
// and combinations are all treated as equivalent to the canonical
// `[mcp_servers.ta]`. Array-of-tables (`[[...]]`) does NOT match a
// standard-table header and is rejected. See QA falsification
// §12.14 MEDIUM-1 finding: whitespace variants were previously
// missed, causing a duplicate canonical block to be appended
// (invalid TOML under the single-instance rule).
func containsTable(doc, header string) bool {
	wantSegs := splitHeaderSegments(header)
	for line := range strings.SplitSeq(doc, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "[") || strings.HasPrefix(trim, "[[") {
			continue
		}
		if !strings.HasSuffix(trim, "]") || strings.HasSuffix(trim, "]]") {
			continue
		}
		inner := trim[1 : len(trim)-1]
		if slices.Equal(wantSegs, splitHeaderSegments(inner)) {
			return true
		}
	}
	return false
}

// splitHeaderSegments splits a dotted TOML key by '.', trimming
// surrounding whitespace per segment and stripping a single pair of
// matching basic or literal quotes. Returns the normalized segment
// list so containsTable can compare against the canonical form.
func splitHeaderSegments(s string) []string {
	parts := strings.Split(s, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) >= 2 && (p[0] == '"' || p[0] == '\'') && p[0] == p[len(p)-1] {
			p = p[1 : len(p)-1]
		}
		out = append(out, p)
	}
	return out
}

// emitInitReport writes either a JSON payload (agent-facing) or a
// laslig success notice + Facts pair (human-facing). The Notice gives
// the semantic SUCCESS marker and the target path; Facts renders the
// structured outcome fields with aligned labels — cleaner than a
// three-line Detail list on the Notice.
func emitInitReport(w io.Writer, r initReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	rr := render.New(w)
	if err := rr.Notice(laslig.NoticeSuccessLevel, "bootstrap complete", r.Path, nil); err != nil {
		return err
	}
	return rr.Facts([]laslig.Field{
		{Label: "schema", Value: r.SchemaSource},
		{Label: "claude", Value: writeLabel(r.ClaudeWritten)},
		{Label: "codex", Value: writeLabel(r.CodexWritten)},
	})
}

func writeLabel(written bool) string {
	if written {
		return "written"
	}
	return "skipped"
}
