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

	"charm.land/huh/v2"
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
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List every db declared in ~/.ta/schema.toml",
		Long:    "Prints the sorted names of every db declared in `~/.ta/schema.toml`. With --json emits `{\"dbs\": [...]}` for agent consumption. Detected legacy `~/.ta/<name>.toml` files (pre-F15 leftovers) trigger a warning notice on stderr.",
		Example: "  ta template list\n  ta template list --json",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			emitLegacyWarning(c.ErrOrStderr())
			names, err := templates.ListDBs()
			if err != nil {
				return err
			}
			if asJSON {
				if names == nil {
					names = []string{}
				}
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"dbs": names})
			}
			root, err := templates.Root()
			if err != nil {
				return err
			}
			return render.New(c.OutOrStdout()).List(root, names, "(no dbs)")
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	return cmd
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
	cmd := &cobra.Command{
		Use:     "show <db>",
		Short:   "Print the bytes of one db from ~/.ta/schema.toml",
		Long:    "Reads `~/.ta/schema.toml`, slices out the named db, and renders its bytes as a glamour-highlighted TOML code block. With --json emits `{\"db\": \"<name>\", \"bytes\": \"<raw>\"}`. A missing db errors loudly with templates.ErrDBNotFound.",
		Example: "  ta template show plans\n  ta template show plans --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			data, err := templates.ShowDB(name)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"db":    name,
					"bytes": string(data),
				})
			}
			return renderTemplateBody(c.OutOrStdout(), name, data)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	return cmd
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
	cmd := &cobra.Command{
		Use:   "save [<db>...]",
		Short: "Merge dbs from <cwd>/.ta/schema.toml into ~/.ta/schema.toml",
		Long: "Reads `<cwd>/.ta/schema.toml`, validates it through the meta-schema, " +
			"and merges the named dbs into `~/.ta/schema.toml`. With no positional " +
			"args, every db declared in the project schema is merged. With one or more " +
			"positional db names, only the named subset is merged. " +
			"Same-name conflicts: on a TTY without `--overwrite`, a single huh.Confirm " +
			"asks whether to replace the conflicting home dbs (yes = all, no = skip " +
			"all). Off-TTY without `--overwrite`, conflicts are auto-skipped. " +
			"`--overwrite` forces replacement on every conflict, no prompt. " +
			"After the merge, the result is re-validated through schema.LoadBytes " +
			"to enforce cross-db invariants (overlapping paths, id collisions); " +
			"a failure leaves `~/.ta/schema.toml` byte-identical.",
		Example: `  ta template save
  ta template save plans
  ta template save plans notes
  ta template save plans --overwrite --json`,
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return runTemplateSave(c.OutOrStdout(), args, overwrite, asJSON)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace same-name dbs in ~/.ta/schema.toml without prompting")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered notice")
	return cmd
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
// to size the huh prompt without performing a write. Loading the
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
	var ok bool
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Affirmative("Overwrite").
			Negative("Skip").
			Value(&ok),
	))
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("save-conflict prompt: %w", err)
	}
	return ok, nil
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
			"If the target already exists, confirms via huh on a TTY or " +
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
	cmd := &cobra.Command{
		Use:   "delete <db>",
		Short: "Remove one db from ~/.ta/schema.toml",
		Long: "Removes the named db from `~/.ta/schema.toml`. Confirms via huh " +
			"on a TTY; requires `--force` off-TTY. Missing dbs error loudly " +
			"with templates.ErrDBNotFound. Removing the last db rewrites the " +
			"file as a comment-only header so subsequent commands see an " +
			"explicit empty-registry state.",
		Example: `  ta template delete old-plans
  ta template delete old-plans --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runTemplateDelete(c.OutOrStdout(), args[0], force, asJSON)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip the huh confirm prompt")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered notice")
	return cmd
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

// ---- shared huh helpers ---------------------------------------------

// promptConfirm is the shared huh.Confirm used by apply/delete (and
// by init via confirmOverwrite, kept separate for title phrasing).
func promptConfirm(title string) (bool, error) {
	var ok bool
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Value(&ok),
	))
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("confirm prompt: %w", err)
	}
	return ok, nil
}
