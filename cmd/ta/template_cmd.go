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
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/fsatomic"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/schema"
	"github.com/evanmschultz/ta/internal/templates"
)

// newTemplateCmd is the parent for `ta template *`. Children are the
// read-only `list` / `show` pair from §12.13 plus the write-side
// `save` / `apply` / `delete` trio from §12.15 / §12.16. Every child
// honors the same TTY-vs-flag discipline as `ta init` (see V2-PLAN
// §14.3 / §14.6).
func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Manage the ~/.ta template library",
		Long: "Inspect and manage the global schema template library at " +
			"`~/.ta/`. Each `.toml` file is one template; `ta init` picks " +
			"from this library to bootstrap a new project.\n\n" +
			"Children: `list` (enumerate), `show <name>` (inspect), " +
			"`save [name]` (promote `<cwd>/.ta/schema.toml` to a template), " +
			"`apply <name> [path]` (write a template into a project), " +
			"`delete <name>` (remove a template).",
		Example:       "  ta template list\n  ta template show schema\n  ta template save\n  ta template apply schema\n  ta template delete old",
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

func newTemplateListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List every template in ~/.ta/",
		Long:    "Prints the sorted names of every `<name>.toml` template under `~/.ta/`. With --json emits `{\"templates\": [...]}` for agent consumption.",
		Example: "  ta template list\n  ta template list --json",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			root, err := templates.Root()
			if err != nil {
				return err
			}
			names, err := templates.List(root)
			if err != nil {
				return err
			}
			if asJSON {
				if names == nil {
					names = []string{}
				}
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"templates": names})
			}
			return render.New(c.OutOrStdout()).List(root, names, "(no templates)")
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	return cmd
}

func newTemplateShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "show <name>",
		Short:   "Print the bytes of one template",
		Long:    "Reads `~/.ta/<name>.toml`, validates it through the schema meta-schema, and renders its bytes as a glamour-highlighted TOML code block. With --json emits `{\"template\": \"<name>\", \"bytes\": \"<raw>\"}`. A malformed template errors loudly per V2-PLAN §14.6.",
		Example: "  ta template show schema\n  ta template show dogfood --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			root, err := templates.Root()
			if err != nil {
				return err
			}
			data, err := templates.Load(root, name)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"template": name,
					"bytes":    string(data),
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
// Mirrors V2-PLAN §14.3 "save [name]" contract.
type templateSaveReport struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Written bool   `json:"written"`
}

func newTemplateSaveCmd() *cobra.Command {
	var force bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "save [name]",
		Short: "Promote <cwd>/.ta/schema.toml to a ~/.ta/<name>.toml template",
		Long: "Reads `<cwd>/.ta/schema.toml`, validates it through the meta-schema, " +
			"and copies the bytes verbatim to `~/.ta/<name>.toml`. With no `name` " +
			"argument on a TTY, prompts via huh. Off-TTY without `name` errors loudly. " +
			"If `~/.ta/<name>.toml` already exists, confirms via huh on a TTY or " +
			"requires `--force` off-TTY. Validation is redundant with " +
			"`templates.Save` (which re-validates internally) — kept to produce a " +
			"line/column error pointing at `<cwd>/.ta/schema.toml` before the " +
			"promotion attempt.",
		Example: `  ta template save
  ta template save dogfood
  ta template save dogfood --force --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			var name string
			if len(args) == 1 {
				name = args[0]
			}
			return runTemplateSave(c.OutOrStdout(), name, force, asJSON)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing template without prompting")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered notice")
	return cmd
}

// runTemplateSave orchestrates the save flow: resolve the source schema,
// validate it, resolve/prompt the template name, honor existing-target
// flow, write, emit report.
func runTemplateSave(out io.Writer, name string, force, asJSON bool) error {
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
	// Pre-validate so a malformed project schema errors with a line/column
	// pointing at sourcePath BEFORE templates.Save runs (which would
	// surface the same error but wrapped with the destination path).
	if _, err := schema.LoadBytes(data); err != nil {
		return fmt.Errorf("save: validate %s: %w", sourcePath, err)
	}

	// nonInteractive gates both the empty-name prompt and the
	// overwrite-confirm. A supplied positional name does NOT belong in
	// this gate: it resolves the empty-name branch (we skip the
	// prompt) but a TTY user still expects to see the confirm when the
	// template already exists. Conflating the two caused the QA
	// falsification §12.16 MEDIUM-2 finding where
	// `ta template save foo` (TTY, target exists, no --force) skipped
	// the confirm and fell to the off-TTY error path.
	nonInteractive := force || asJSON
	if name == "" {
		if !ttyInteractive(nonInteractive) {
			return errors.New("save: no template name supplied; pass it as a positional arg or run on a TTY for the prompt")
		}
		picked, err := promptTemplateName()
		if err != nil {
			return err
		}
		name = picked
	}

	root, err := templates.Root()
	if err != nil {
		return err
	}
	destPath := filepath.Join(root, name+".toml")
	if _, err := os.Stat(destPath); err == nil {
		switch {
		case force:
			// fall through to overwrite
		case ttyInteractive(nonInteractive):
			ok, err := promptConfirm(fmt.Sprintf("Overwrite existing template %q?", name))
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("save: template %q exists; aborted (pass --force to overwrite without prompt)", name)
			}
		default:
			return fmt.Errorf("save: template %q exists; pass --force to overwrite", name)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", destPath, err)
	}

	if err := templates.Save(root, name, data); err != nil {
		return err
	}
	report := templateSaveReport{Name: name, Source: sourcePath, Written: true}
	return emitTemplateSaveReport(out, report, asJSON)
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
	return render.New(w).Success("ta template save", r.Name, detail)
}

// ---- apply -----------------------------------------------------------

// templateApplyReport is the --json emit shape for `ta template apply`.
// Mirrors V2-PLAN §14.3 "apply <name> [path]" contract.
type templateApplyReport struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	Written bool   `json:"written"`
}

func newTemplateApplyCmd() *cobra.Command {
	var force bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "apply <name> [path]",
		Short: "Copy ~/.ta/<name>.toml into <path>/.ta/schema.toml",
		Long: "Writes the template bytes verbatim to `<path>/.ta/schema.toml`. " +
			"Target path defaults to cwd; if supplied, MUST be absolute " +
			"(matches `ta init`). Creates the `.ta/` directory if missing. " +
			"If the target already exists, confirms via huh on a TTY or " +
			"requires `--force` off-TTY. Schema-only — does NOT touch " +
			"`.mcp.json` / `.codex/config.toml` (use `ta init` for a full " +
			"bootstrap) per V2-PLAN §14.3.",
		Example: `  ta template apply schema
  ta template apply schema /abs/path/proj
  ta template apply schema /abs/path --force --json`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			var targetArg string
			if len(args) == 2 {
				targetArg = args[1]
			}
			return runTemplateApply(c.OutOrStdout(), name, targetArg, force, asJSON)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing <path>/.ta/schema.toml without prompting")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered notice")
	return cmd
}

func runTemplateApply(out io.Writer, name, targetArg string, force, asJSON bool) error {
	target, err := resolveApplyPath(targetArg)
	if err != nil {
		return err
	}
	root, err := templates.Root()
	if err != nil {
		return err
	}
	data, err := templates.Load(root, name)
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

// resolveApplyPath mirrors resolveInitPath's discipline: no arg → cwd;
// arg → must be absolute. Keeps agent invocations independent of the
// shell's cwd per V2-PLAN §14.3.
func resolveApplyPath(arg string) (string, error) {
	if arg == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		return cwd, nil
	}
	if !filepath.IsAbs(arg) {
		return "", fmt.Errorf("apply: path must be absolute; got %q", arg)
	}
	return filepath.Clean(arg), nil
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
		Use:   "delete <name>",
		Short: "Remove a template from ~/.ta/",
		Long: "Removes `~/.ta/<name>.toml`. Confirms via huh on a TTY; " +
			"requires `--force` off-TTY. Missing templates error loudly.",
		Example: `  ta template delete old-schema
  ta template delete old-schema --force`,
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
	root, err := templates.Root()
	if err != nil {
		return err
	}
	// Check existence up front so the "missing" error is loud, matching
	// the templates.Delete behavior but letting us produce a cleaner
	// message before the confirm prompt even runs.
	destPath := filepath.Join(root, name+".toml")
	if _, err := os.Stat(destPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("delete: template %q not found at %s", name, destPath)
		}
		return fmt.Errorf("stat %s: %w", destPath, err)
	}

	nonInteractive := force || asJSON
	switch {
	case force:
		// fall through to delete
	case ttyInteractive(nonInteractive):
		ok, err := promptConfirm(fmt.Sprintf("Delete template %q?", name))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("delete: aborted; template %q left in place", name)
		}
	default:
		return fmt.Errorf("delete: template %q requires --force off a TTY", name)
	}

	if err := templates.Delete(root, name); err != nil {
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

// promptTemplateName runs a huh.Input for the new template name.
func promptTemplateName() (string, error) {
	var name string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Save as template name:").
			Value(&name),
	))
	if err := form.Run(); err != nil {
		return "", fmt.Errorf("name prompt: %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("save: empty template name")
	}
	return name, nil
}

// promptConfirm is the shared huh.Confirm used by save/apply/delete.
// init_cmd.go has its own confirmOverwrite; kept separate because the
// title phrasing differs per command.
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
