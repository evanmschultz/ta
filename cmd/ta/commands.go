package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/schema"
)

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <path> <section>",
		Short: "Read a TOML section by bracket path, print raw bytes to stdout",
		Long: "Mirrors the MCP tool `get`. Writes the raw TOML bytes of the " +
			"section — leading comment block, header line, and body — to stdout " +
			"exactly as they appear in the file.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, section := args[0], args[1]
			f, err := toml.Parse(path)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			sec, ok := f.Find(section)
			if !ok {
				return fmt.Errorf("section %q not found in %s", section, path)
			}
			_, err = c.OutOrStdout().Write(f.Buf[sec.Range[0]:sec.Range[1]])
			return err
		},
	}
}

func newListSectionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-sections <path>",
		Short: "Enumerate every section in a TOML file, in file order",
		Long: "Mirrors the MCP tool `list_sections`. When the target file does " +
			"not exist yet, the list is empty rather than erroring — matches " +
			"the MCP behavior callers already depend on.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path := args[0]
			f, err := toml.Parse(path)
			if err != nil && !errors.Is(err, toml.ErrNotExist) {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			var paths []string
			if f != nil {
				paths = f.Paths()
			}
			items := make([]laslig.ListItem, len(paths))
			for i, sp := range paths {
				items[i] = laslig.ListItem{Title: sp}
			}
			p := laslig.New(c.OutOrStdout(), humanPolicy())
			return p.List(laslig.List{
				Title: path,
				Items: items,
				Empty: "(no sections)",
			})
		},
	}
}

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema <path> [section]",
		Short: "Show the resolved schema for a TOML file (glamour-rendered markdown)",
		Long: "Mirrors the MCP tool `schema`. Without a section arg, renders " +
			"every type in the cascade-merged registry. With a dot-notated " +
			"section arg (e.g. `task.task_001`), renders just the type matched " +
			"by the first segment.",
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path := args[0]
			var section string
			if len(args) == 2 {
				section = args[1]
			}
			resolution, err := config.Resolve(path)
			if err != nil {
				return fmt.Errorf("resolve schema for %s: %w", path, err)
			}
			types := resolution.Registry.Types
			if section != "" {
				t, ok := resolution.Registry.Lookup(section)
				if !ok {
					return fmt.Errorf("no schema registered for section %q in %s", section, path)
				}
				types = map[string]schema.SectionType{t.Name: t}
			}
			return renderSchemaMarkdown(c.OutOrStdout(), path, section, resolution.Sources, types)
		},
	}
}

func newUpsertCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	cmd := &cobra.Command{
		Use:   "upsert <path> <section>",
		Short: "Create or update a section, validated against the resolved schema",
		Long: "Mirrors the MCP tool `upsert`. Provide the section's fields as " +
			"a JSON object via --data (inline) or --data-file <path> " +
			"(`-` reads from stdin). Untouched bytes in the target file — " +
			"including comments, blank lines, and other sections — are " +
			"preserved byte-for-byte.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, section := args[0], args[1]

			raw, err := readUpsertData(dataInline, dataFile, c.InOrStdin())
			if err != nil {
				return err
			}
			var data map[string]any
			if err := json.Unmarshal(raw, &data); err != nil {
				return fmt.Errorf("parse data JSON: %w", err)
			}

			resolution, err := config.Resolve(path)
			if err != nil {
				return fmt.Errorf("resolve schema for %s: %w", path, err)
			}
			if err := resolution.Registry.Validate(section, data); err != nil {
				return err
			}

			f, err := toml.Parse(path)
			if err != nil {
				if !errors.Is(err, toml.ErrNotExist) {
					return fmt.Errorf("parse %s: %w", path, err)
				}
				f = &toml.File{Path: path}
			}
			emitted, err := toml.EmitSection(section, data)
			if err != nil {
				return fmt.Errorf("emit %q: %w", section, err)
			}
			newBuf, err := f.Splice(section, emitted)
			if err != nil {
				return fmt.Errorf("splice %q: %w", section, err)
			}
			if err := toml.WriteAtomic(path, newBuf); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}

			p := laslig.New(c.OutOrStdout(), humanPolicy())
			return p.Notice(laslig.Notice{
				Level:  laslig.NoticeSuccessLevel,
				Title:  fmt.Sprintf("upserted %s", section),
				Body:   path,
				Detail: resolution.Sources,
			})
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	return cmd
}

func readUpsertData(inline, file string, stdin io.Reader) ([]byte, error) {
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

func renderSchemaMarkdown(w io.Writer, path, section string, sources []string, types map[string]schema.SectionType) error {
	var sb strings.Builder
	if section != "" {
		fmt.Fprintf(&sb, "# Schema for `%s` (section `%s`)\n\n", path, section)
	} else {
		fmt.Fprintf(&sb, "# Schema for `%s`\n\n", path)
	}
	if len(sources) > 0 {
		sb.WriteString("**Resolved from:**\n\n")
		for _, s := range sources {
			fmt.Fprintf(&sb, "- `%s`\n", s)
		}
		sb.WriteString("\n")
	}
	names := make([]string, 0, len(types))
	for n := range types {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t := types[name]
		fmt.Fprintf(&sb, "## `%s`\n\n", name)
		if t.Description != "" {
			sb.WriteString(t.Description + "\n\n")
		}
		sb.WriteString("| field | type | required | default | description |\n")
		sb.WriteString("|---|---|---|---|---|\n")
		fieldNames := make([]string, 0, len(t.Fields))
		for fn := range t.Fields {
			fieldNames = append(fieldNames, fn)
		}
		sort.Strings(fieldNames)
		for _, fn := range fieldNames {
			f := t.Fields[fn]
			req := ""
			if f.Required {
				req = "yes"
			}
			def := ""
			if f.Default != nil {
				def = fmt.Sprintf("`%v`", f.Default)
			}
			desc := strings.ReplaceAll(f.Description, "|", `\|`)
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s | %s |\n", fn, f.Type, req, def, desc)
		}
		sb.WriteString("\n")
	}

	p := laslig.New(w, humanPolicy())
	return p.Markdown(laslig.Markdown{Body: sb.String()})
}
