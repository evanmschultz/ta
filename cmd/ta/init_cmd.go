package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"
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
		Use:   "init [path]",
		Short: "Bootstrap a project directory with a schema and MCP configs",
		Long: "Bootstrap a project directory from the `~/.ta/` template " +
			"library. With a TTY and no flags, runs an interactive huh " +
			"picker; with flags, runs non-interactively. Writes " +
			"`<path>/.ta/schema.toml` from the chosen template (or an empty " +
			"header for --blank), and by default writes `<path>/.mcp.json` " +
			"(Claude Code) and `<path>/.codex/config.toml` (Codex). Per-path " +
			"defaults can be set in `<path>/.ta/config.toml` (V2-PLAN §14.5); " +
			"`ta init` does NOT create that file itself — edit it by hand to " +
			"tune future `ta init` runs on the same path.",
		Example: "  ta init\n  ta init /abs/path/to/new-project --template schema\n  ta init /abs/path --template schema --no-codex --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			target, err := resolveInitPath(args)
			if err != nil {
				return err
			}
			f.nonInterRq = f.template != "" || f.blank
			return runInit(c.OutOrStdout(), c.InOrStdin(), target, f)
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
	return cmd
}

// resolveInitPath turns an optional arg into an absolute path. With no
// arg, defaults to cwd. With an arg, requires absolute per V2-PLAN
// §14.3 "no relative paths" rule — explicit so agent invocations do
// not depend on the shell's cwd.
func resolveInitPath(args []string) (string, error) {
	if len(args) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		return cwd, nil
	}
	p := args[0]
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("init: path must be absolute; got %q", p)
	}
	return filepath.Clean(p), nil
}

// runInit orchestrates bootstrap: mkdir -p the target, resolve
// template choice (flag or picker), validate schema write (force /
// confirm path), write schema, then write the two MCP configs honoring
// flags and `<path>/.ta/config.toml`.
func runInit(out io.Writer, in io.Reader, target string, f initFlags) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}

	bootCfg, err := readBootstrapConfig(target)
	if err != nil {
		return err
	}

	effClaude, effCodex := effectiveMCPToggles(f, bootCfg)

	schemaSource, schemaBytes, err := chooseSchema(in, out, f, bootCfg)
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
func chooseSchema(in io.Reader, out io.Writer, f initFlags, cfg bootstrapConfig) (string, []byte, error) {
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

	choice, err := pickTemplate(names, cfg.Bootstrap.DefaultTemplate)
	if err != nil {
		return "", nil, err
	}
	if choice == blankTemplateChoice {
		return "blank", []byte(blankSchemaBody), nil
	}
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
	if def != "" {
		// Pre-select the default by seeding the bound variable; huh
		// uses the initial value to highlight the matching option.
		for _, n := range names {
			if n == def {
				choice = def
				break
			}
		}
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
	if f.nonInterRq {
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
func containsTable(doc, header string) bool {
	want := "[" + header + "]"
	for _, line := range strings.Split(doc, "\n") {
		trim := strings.TrimSpace(line)
		if trim == want {
			return true
		}
	}
	return false
}

// emitInitReport writes either a JSON payload (agent-facing) or a
// laslig success notice (human-facing).
func emitInitReport(w io.Writer, r initReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	detail := []string{
		fmt.Sprintf("schema source: %s", r.SchemaSource),
		fmt.Sprintf("claude (.mcp.json): %s", writeLabel(r.ClaudeWritten)),
		fmt.Sprintf("codex (.codex/config.toml): %s", writeLabel(r.CodexWritten)),
	}
	return render.New(w).Success("ta init", r.Path, detail)
}

func writeLabel(written bool) string {
	if written {
		return "written"
	}
	return "skipped"
}
