// Command ta runs the ta MCP server, exposing TOML files to MCP clients as
// schema-validated, agent-accessible data.
//
// The CLI is built on charmbracelet/fang (cobra-based) with
// evanmschultz/laslig handling pretty-printed user-facing output (startup
// banners, error notices, and glamour-styled markdown blocks). Anything
// emitted by laslig or fang goes to stderr while the bare root command is
// serving MCP — stdout is reserved for the MCP protocol stream.
//
// See docs/ta.md for the design and docs/PLAN.md for the MVP build plan.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	fang "charm.land/fang/v2"
	"charm.land/huh/v2"
	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/mcpsrv"
	"github.com/evanmschultz/ta/internal/render"
)

const appName = "ta"

const longDescription = "# ta\n\n" +
	"Tiny MCP server (and matching CLI) that exposes TOML and Markdown " +
	"files as schema-validated, agent-accessible structured data.\n\n" +
	"Running `ta` with no subcommand is dual-mode:\n\n" +
	"- On an interactive terminal (TTY), shows a huh picker of the available " +
	"subcommands.\n" +
	"- When spawned by an MCP client (e.g. Claude Code — stdio pipes, no TTY), " +
	"starts the MCP server. Register it via `.mcp.json` or `claude mcp add`; " +
	"the stdio handshake makes the server path fire automatically.\n\n" +
	"The same tool surface is available as terminal subcommands (every " +
	"path-taking command takes `--path` as a flag; default cwd, relative " +
	"or absolute accepted — V2-PLAN §12.17.5 [A1]):\n\n" +
	"- `ta get <section>` — read a record; optionally --fields name[,name]\n" +
	"- `ta list-sections [scope]` — enumerate record addresses under a scope\n" +
	"- `ta schema [section]` — show the resolved schema\n" +
	"- `ta create <section> --data <json>` — create a new record\n" +
	"- `ta update <section> --data <json>` — update an existing record\n" +
	"- `ta delete <section>` — remove a record, file, or instance dir\n" +
	"- `ta init` — bootstrap a project directory (schema + MCP configs)\n" +
	"- `ta template (list|show|save|apply|delete)` — manage the ~/.ta library\n\n" +
	"Each project has a self-contained schema at `<project>/.ta/schema.toml`. " +
	"The runtime reads exactly that one file — no home-layer cascade, no " +
	"ancestor walk."

func main() {
	err := fang.Execute(
		context.Background(),
		newRootCmd(),
		fang.WithVersion(version()),
		fang.WithCommit(commitRev()),
		fang.WithNotifySignal(os.Interrupt),
		fang.WithoutCompletions(),
	)
	if err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var logStartup bool
	cmd := &cobra.Command{
		Use:   appName,
		Short: "MCP server (and matching CLI) for schema-validated TOML",
		Long:  longDescription,
		Example: `  ta init --path /abs/path/to/new-project
  ta get plans.task.task-001
  ta template list`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runRoot(c, logStartup)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&logStartup, "log-startup", false, "log a startup banner to stderr before serving")
	cmd.AddCommand(
		newGetCmd(),
		newListSectionsCmd(),
		newCreateCmd(),
		newUpdateCmd(),
		newDeleteCmd(),
		newSchemaCmd(),
		newSearchCmd(),
		newTemplateCmd(),
		newInitCmd(),
	)
	// Custom help command so `ta h` and `ta h <cmd>` work in addition
	// to cobra's default `ta help [cmd]`. Mirrors cobra's default help
	// behavior (walk args against root, print target's Help, fall back
	// to root usage on unknown target). V2-PLAN §14.7.
	//
	// The `target == nil || err != nil` guard mirrors the defense in
	// cobra's own InitDefaultHelpCmd. Under the current root config
	// (Args: cobra.NoArgs) Find returns `(root, leftover, nil)` even
	// for unknown topics, so the guard is unreachable in practice —
	// but keeping it aligns with stock cobra and guards against a
	// future Args change that would make Find return nil/err.
	cmd.SetHelpCommand(&cobra.Command{
		Use:     "help [command]",
		Short:   "Help about any command",
		Aliases: []string{"h"},
		Run: func(c *cobra.Command, args []string) {
			target, _, err := c.Root().Find(args)
			if target == nil || err != nil {
				c.Printf("unknown help topic %q\n", args)
				_ = c.Root().Usage()
				return
			}
			target.InitDefaultHelpFlag()
			target.InitDefaultVersionFlag()
			_ = target.Help()
		},
	})
	// Register the help command eagerly so `Find`-based lookups (tests,
	// the huh root menu) see it without having to invoke Execute first.
	// cobra's Execute would otherwise call this lazily.
	cmd.InitDefaultHelpCmd()
	return cmd
}

// runRoot is the bare-`ta` entrypoint. Dual behavior per V2-PLAN §14.3:
//
//   - BOTH stdin and stdout are TTYs → launch a huh select over the
//     available subcommands. The chosen subcommand runs with `--help` so
//     the user sees usage + Example for the picked command, because most
//     subcommands require positional args that the picker cannot collect.
//   - EITHER stdin or stdout is NOT a TTY → start the MCP server over
//     stdio, unchanged from the pre-§12.16 behavior. MCP clients spawn
//     `ta` with stdio pipes (not TTYs), so this keeps existing
//     `.mcp.json` / `claude mcp add` invocations working byte-identically.
func runRoot(cmd *cobra.Command, logStartup bool) error {
	if ttyInteractive(false) {
		return runMenu(cmd)
	}
	return runServe(cmd.Context(), cmd.ErrOrStderr(), logStartup)
}

// runMenu presents a huh.Select over the root's subcommand names and
// then invokes the selected command with `--help`. Help is the right
// default on a discovery menu: most subcommands require positional
// args + flags, so the user picks from the menu, reads what the
// command needs, and re-runs with the right invocation.
func runMenu(root *cobra.Command) error {
	items := menuItems(root)
	if len(items) == 0 {
		return fmt.Errorf("no subcommands registered on root")
	}
	opts := make([]huh.Option[string], 0, len(items))
	for _, it := range items {
		label := it.name + " — " + it.short
		opts = append(opts, huh.NewOption(label, it.name))
	}
	var chosen string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("ta — pick a subcommand").
			Options(opts...).
			Value(&chosen),
	))
	if err := form.Run(); err != nil {
		return fmt.Errorf("menu: %w", err)
	}
	if chosen == "" {
		return fmt.Errorf("menu: no subcommand selected")
	}
	if _, _, err := root.Find([]string{chosen}); err != nil {
		return fmt.Errorf("menu: resolve %q: %w", chosen, err)
	}
	// Re-execute through the root so cobra's usage template fires and
	// the command's Example / flag docs render via fang's styling.
	root.SetArgs([]string{chosen, "--help"})
	return root.Execute()
}

// menuItem is one row in the bare-`ta` huh menu.
type menuItem struct {
	name  string
	short string
}

// menuItems enumerates root subcommands eligible for the menu. Hidden
// and `completion` commands (if any) are skipped. Ordering follows
// cobra's registration order so the menu's top entry stays stable.
func menuItems(root *cobra.Command) []menuItem {
	var items []menuItem
	for _, sub := range root.Commands() {
		if sub.Hidden {
			continue
		}
		if sub.Name() == "completion" || sub.Name() == "help" {
			continue
		}
		items = append(items, menuItem{name: sub.Name(), short: sub.Short})
	}
	return items
}

func runServe(ctx context.Context, stderr io.Writer, logStartup bool) error {
	// Post-V2-PLAN §12.11 / §14.9, mcpsrv.Config.ProjectPath is
	// required. Bare `ta` without a TTY is spawned by an MCP client
	// whose stdio handshake wrapper sets cwd to the project root, so
	// os.Getwd() is the canonical project path here.
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	srv, err := mcpsrv.New(mcpsrv.Config{
		Name:        appName,
		Version:     version(),
		ProjectPath: cwd,
	})
	if err != nil {
		return err
	}
	if logStartup {
		renderStartupNotice(stderr)
	}
	return srv.Run(ctx)
}

func renderStartupNotice(w io.Writer) {
	_ = render.New(w).Notice(
		laslig.NoticeInfoLevel,
		appName+" "+version()+" ready",
		"serving MCP over stdio",
		nil,
	)
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown)"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	if rev := vcsSetting(info, "vcs.revision"); rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		return "(devel " + rev + ")"
	}
	return "(devel)"
}

func commitRev() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return vcsSetting(info, "vcs.revision")
}

func vcsSetting(info *debug.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}
