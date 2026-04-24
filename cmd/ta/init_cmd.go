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
	"github.com/evanmschultz/ta/internal/templates"
)

// blankSchemaBody is the one-comment header written for `--blank`.
// Minimal but non-empty so `ta schema get` can open it cleanly.
const blankSchemaBody = "# ta schema — ready for declarations\n"

// blankTemplateChoice is the sentinel value the huh picker uses for
// the "start from scratch" option; it maps to the --blank flag path.
const blankTemplateChoice = "<blank>"

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
	blank      bool
	noClaude   bool
	noCodex    bool
	force      bool
	asJSON     bool
	nonInterRq bool // non-interactive because of flags, not TTY absence
}

// initReport is the structured outcome of `ta init`. The --json emit
// shape mirrors it verbatim (V2-PLAN §14 bootstrap contract).
type initReport struct {
	Path          string `json:"path"`
	SchemaSource  string `json:"schema_source"` // "<template-name>" or "blank"
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
		Long: "Bootstrap a project directory from the `~/.ta/` template " +
			"library. With a TTY and no flags, runs an interactive huh " +
			"picker; with flags, runs non-interactively. Writes " +
			"`<path>/.ta/schema.toml` from the chosen template (or an empty " +
			"header for --blank), and by default writes `<path>/.mcp.json` " +
			"(Claude Code) and `<path>/.codex/config.toml` (Codex). Per-path " +
			"defaults can be set in `<path>/.ta/config.toml` (V2-PLAN §14.5); " +
			"`ta init` does NOT create that file itself — edit it by hand to " +
			"tune future `ta init` runs on the same path. --path defaults to " +
			"cwd; relative or absolute accepted (V2-PLAN §12.17.5 [A1]).",
		Example: "  ta init\n  ta init --path /abs/path/to/new-project --template schema\n  ta init --path /abs/path --template schema --no-codex --json",
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
			f.nonInterRq = f.template != "" || f.blank || f.asJSON
			return runInit(c.OutOrStdout(), c.ErrOrStderr(), c.InOrStdin(), target, f)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().StringVar(&f.template, "template", "", "name of a template under ~/.ta/ (skips huh picker)")
	cmd.Flags().BoolVar(&f.blank, "blank", false, "write an empty schema (skips huh picker; mutually exclusive with --template)")
	cmd.Flags().BoolVar(&f.noClaude, "no-claude", false, "skip .mcp.json generation")
	cmd.Flags().BoolVar(&f.noCodex, "no-codex", false, "skip .codex/config.toml generation")
	cmd.Flags().BoolVar(&f.force, "force", false, "overwrite an existing .ta/schema.toml without prompting")
	cmd.Flags().BoolVar(&f.asJSON, "json", false, "emit JSON instead of laslig-rendered notices")
	cmd.MarkFlagsMutuallyExclusive("template", "blank")
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

// chooseSchema resolves which schema bytes to write: explicit flag
// (--template / --blank), bootstrap default, or huh picker on TTY.
// Returns the source label used for the report ("<name>" or "blank").
// `errOut` receives per-template warnings when the picker path encounters
// a malformed entry (typical case: legacy pre-v2 `~/.ta/schema.toml`);
// the malformed template is filtered out of the picker, the user sees
// the warning on stderr, and the rest of the library still shows up.
func chooseSchema(in io.Reader, out, errOut io.Writer, f initFlags, cfg bootstrapConfig) (string, []byte, error) {
	if f.blank {
		return "blank", []byte(blankSchemaBody), nil
	}
	if f.template != "" {
		data, err := loadTemplate(f.template)
		if err != nil {
			return "", nil, err
		}
		return f.template, data, nil
	}

	// No explicit flag. On a TTY run the picker; off-TTY honor bootstrap
	// default if set, else error loudly (non-interactive with no
	// template selection is ambiguous).
	if !interactive(in, out, f) {
		if cfg.Bootstrap.DefaultTemplate != "" {
			data, err := loadTemplate(cfg.Bootstrap.DefaultTemplate)
			if err != nil {
				return "", nil, err
			}
			return cfg.Bootstrap.DefaultTemplate, data, nil
		}
		return "", nil, errors.New("init: no template selected; pass --template <name>, --blank, or run on a TTY for the picker")
	}

	root, err := templates.Root()
	if err != nil {
		return "", nil, err
	}
	names, err := templates.List(root)
	if err != nil {
		return "", nil, err
	}

	// Validate each candidate once. A template that fails schema
	// validation (e.g. a legacy pre-MVP `~/.ta/schema.toml` left over
	// from the old cascade era) is filtered out of the picker with a
	// styled warning so a single bad file does not block bootstrap.
	// Cache bytes so the post-pick path does not re-read and re-parse.
	validNames := make([]string, 0, len(names))
	cache := make(map[string][]byte, len(names))
	var invalid []string
	warn := render.New(errOut)
	for _, n := range names {
		data, err := templates.Load(root, n)
		if err != nil {
			_ = warn.Notice(
				laslig.NoticeWarningLevel,
				"malformed template skipped",
				fmt.Sprintf("~/.ta/%s.toml is not a valid schema", n),
				[]string{
					fmt.Sprintf("delete: ta template delete %s", n),
					"or fix: add file=, directory=, or collection= at the top of the file",
				},
			)
			invalid = append(invalid, n)
			continue
		}
		validNames = append(validNames, n)
		cache[n] = data
	}

	// Offer inline deletion of malformed templates so the user can act
	// on the warnings without exiting the picker first. Only fires on
	// a TTY — non-interactive flows (--json, off-TTY) just see the
	// warnings and move on.
	if len(invalid) > 0 {
		if ok, err := promptDeleteMalformed(invalid); err != nil {
			return "", nil, err
		} else if ok {
			deleted := deleteMalformed(errOut, root, invalid)
			summarizeMalformedDelete(warn, deleted, len(invalid))
		}
	}

	choice, err := pickTemplate(validNames, cfg.Bootstrap.DefaultTemplate)
	if err != nil {
		return "", nil, err
	}
	if choice == blankTemplateChoice {
		return "blank", []byte(blankSchemaBody), nil
	}
	if data, ok := cache[choice]; ok {
		return choice, data, nil
	}
	// Defensive fallback: picker somehow returned a name we did not
	// validate. Re-load directly — validation will fire again.
	data, err := loadTemplate(choice)
	if err != nil {
		return "", nil, err
	}
	return choice, data, nil
}

func loadTemplate(name string) ([]byte, error) {
	root, err := templates.Root()
	if err != nil {
		return nil, err
	}
	return templates.Load(root, name)
}

// pickTemplate runs the huh single-select over the library names plus
// the <blank> option. Pre-selects the bootstrap-config default if set.
func pickTemplate(names []string, def string) (string, error) {
	opts := make([]huh.Option[string], 0, len(names)+1)
	for _, n := range names {
		opts = append(opts, huh.NewOption(n, n))
	}
	opts = append(opts, huh.NewOption(blankTemplateChoice, blankTemplateChoice))

	var choice string
	if def != "" && slices.Contains(names, def) {
		// Pre-select the default by seeding the bound variable; huh
		// uses the initial value to highlight the matching option.
		choice = def
	}
	sel := huh.NewSelect[string]().
		Title("Pick a schema template").
		Options(opts...).
		Value(&choice)
	form := huh.NewForm(huh.NewGroup(sel))
	if err := form.Run(); err != nil {
		return "", fmt.Errorf("template picker: %w", err)
	}
	if choice == "" {
		return "", errors.New("init: no template chosen")
	}
	return choice, nil
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

// promptDeleteMalformed asks whether to remove the set of malformed
// templates identified during the picker scan. Runs a single huh
// Confirm sized to the count — one-off wording for a single entry
// reads better than the generic plural form.
func promptDeleteMalformed(names []string) (bool, error) {
	title := fmt.Sprintf("Delete %d malformed template(s)?", len(names))
	if len(names) == 1 {
		title = fmt.Sprintf("Delete malformed template %q?", names[0])
	}
	var ok bool
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Affirmative("Delete").
			Negative("Skip").
			Value(&ok),
	))
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("delete-malformed prompt: %w", err)
	}
	return ok, nil
}

// deleteMalformed removes each named template from root. Failures are
// logged to errOut but do NOT abort the sweep — a permission error on
// one template should not block deleting the others. Returns the count
// of successful deletions.
func deleteMalformed(errOut io.Writer, root string, names []string) int {
	var deleted int
	for _, n := range names {
		if err := templates.Delete(root, n); err != nil {
			fmt.Fprintf(errOut, "failed to delete %q: %v\n", n, err)
			continue
		}
		deleted++
	}
	return deleted
}

// summarizeMalformedDelete emits one laslig Notice reporting the
// outcome of the sweep. Three cases cover all delete-count arithmetic:
// success (every template removed), partial (some removed, some
// failed), and failure (zero removed). Count-aware noun matches the
// reported number; pluralization is explicit so `1 template` does not
// render as `1 template(s)`.
func summarizeMalformedDelete(warn *render.Renderer, deleted, total int) {
	noun := pluralize("template", deleted)
	switch {
	case deleted == total:
		_ = warn.Notice(
			laslig.NoticeSuccessLevel,
			"malformed "+pluralize("template", total)+" removed",
			fmt.Sprintf("deleted %d %s from ~/.ta/", deleted, noun),
			nil,
		)
	case deleted > 0:
		_ = warn.Notice(
			laslig.NoticeWarningLevel,
			"partial delete",
			fmt.Sprintf("removed %d of %d; see stderr for per-template failures", deleted, total),
			nil,
		)
	default:
		_ = warn.Notice(
			laslig.NoticeErrorLevel,
			"delete failed",
			fmt.Sprintf("none of the %d malformed %s could be removed; see stderr for details", total, pluralize("template", total)),
			nil,
		)
	}
}

// pluralize returns noun or noun+"s" based on count. Simple English
// pluralization — one-off call site, not worth importing a helper.
func pluralize(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
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
// no non-interactive flag (--template / --blank) was set, so the
// picker is both possible and wanted.
func interactive(_ io.Reader, _ io.Writer, f initFlags) bool {
	return ttyInteractive(f.nonInterRq)
}

// ttyInteractive is the shared TTY-vs-flags gate used by `ta init` and
// every `ta template *` write subcommand. Returns true only when both
// stdin AND stdout are TTYs AND the caller has NOT forced a
// non-interactive path via flags (e.g. `--force`, `--json`,
// `--template`, `--blank`). Matching `os.Stdin` / `os.Stdout` keeps
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
