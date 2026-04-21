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
	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/mcpsrv"
)

const appName = "ta"

const longDescription = "# ta\n\n" +
	"Tiny MCP server that exposes TOML files as schema-validated, " +
	"agent-accessible structured data. Runs over **stdio** — point an MCP " +
	"client (e.g. Claude Code) at the binary and it exposes four tools:\n\n" +
	"- `get` — read a TOML section by bracket path\n" +
	"- `list_sections` — list every section in a file\n" +
	"- `schema` — show the resolved schema (whole file or one section type)\n" +
	"- `upsert` — create or update a section, validated against config\n\n" +
	"Schemas resolve by cascade-merge: `~/.ta/config.toml` is the base layer, " +
	"and every `.ta/config.toml` on the target file's ancestor chain is " +
	"folded on top — same-named section types override, unique types are " +
	"additive. All tool arguments arrive over MCP; the binary itself reads " +
	"only its CLI flags."

func main() {
	err := fang.Execute(
		context.Background(),
		newRootCmd(),
		fang.WithVersion(version()),
		fang.WithCommit(commitRev()),
		fang.WithNotifySignal(os.Interrupt),
		fang.WithErrorHandler(renderErrorHandler),
	)
	if err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var logStartup bool
	cmd := &cobra.Command{
		Use:   appName,
		Short: "MCP server exposing TOML files as schema-validated structured data",
		Long:  longDescription,
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runServe(c.Context(), c.ErrOrStderr(), logStartup)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&logStartup, "log-startup", false, "log a startup banner to stderr before serving")
	return cmd
}

func runServe(ctx context.Context, stderr io.Writer, logStartup bool) error {
	srv, err := mcpsrv.New(mcpsrv.Config{Name: appName, Version: version()})
	if err != nil {
		return err
	}
	if logStartup {
		renderStartupNotice(stderr)
	}
	return srv.Run(ctx)
}

func renderStartupNotice(w io.Writer) {
	p := laslig.New(w, humanPolicy())
	_ = p.Notice(laslig.Notice{
		Level: laslig.NoticeInfoLevel,
		Title: fmt.Sprintf("%s %s ready", appName, version()),
		Body:  "serving MCP over stdio",
	})
}

func renderErrorHandler(w io.Writer, _ fang.Styles, err error) {
	p := laslig.New(w, humanPolicy())
	_ = p.Notice(laslig.Notice{
		Level: laslig.NoticeErrorLevel,
		Title: appName,
		Body:  err.Error(),
	})
}

func humanPolicy() laslig.Policy {
	return laslig.Policy{
		Format:       laslig.FormatAuto,
		Style:        laslig.StyleAuto,
		GlamourStyle: laslig.DefaultGlamourStyle(),
	}
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
