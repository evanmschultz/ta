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
	"io"
	"os"
	"runtime/debug"

	fang "charm.land/fang/v2"
	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/mcpsrv"
	"github.com/evanmschultz/ta/internal/render"
)

const appName = "ta"

const longDescription = "# ta\n\n" +
	"Tiny MCP server (and matching CLI) that exposes TOML and Markdown " +
	"files as schema-validated, agent-accessible structured data. " +
	"Running `ta` with no subcommand starts the MCP server over **stdio** — " +
	"point an MCP client (e.g. Claude Code) at the binary.\n\n" +
	"The same tool surface is available as terminal subcommands:\n\n" +
	"- `ta get <path> <section>` — read a record; optionally --fields name[,name]\n" +
	"- `ta list-sections <path>` — enumerate sections in a TOML file\n" +
	"- `ta schema <path> [section]` — show the resolved schema\n" +
	"- `ta create <path> <section> --data <json>` — create a new record\n" +
	"- `ta update <path> <section> --data <json>` — update an existing record\n" +
	"- `ta delete <path> <section>` — remove a record, file, or instance dir\n\n" +
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
		fang.WithErrorHandler(renderErrorHandler),
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
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runServe(c.Context(), c.ErrOrStderr(), logStartup)
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
	)
	return cmd
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

func renderErrorHandler(w io.Writer, _ fang.Styles, err error) {
	_ = render.New(w).Error(appName, err)
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
