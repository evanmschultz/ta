package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/templates"
)

// newTemplateCmd is the parent for `ta template *`. This slice lands
// the read-only children (`list`, `show`). V2-PLAN §12.15/§12.16 add
// `save`, `apply`, `delete` later.
func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Inspect the ~/.ta template library",
		Long: "Read-only view of the global schema template library at " +
			"`~/.ta/`. Each `.toml` file is one template; `ta init` picks " +
			"from this library to bootstrap a new project. Run `ta template " +
			"list` to enumerate available templates and `ta template show " +
			"<name>` to inspect one.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newTemplateListCmd(), newTemplateShowCmd())
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
