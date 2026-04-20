// Command ta runs the ta MCP server, exposing TOML files to MCP clients as
// schema-validated, agent-accessible data.
//
// See docs/ta.md for the design and docs/PLAN.md for the MVP build plan.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/evanmschultz/laslig"

	"github.com/evanmschultz/ta/internal/mcpsrv"
)

const appName = "ta"

const helpBody = `ta speaks MCP over stdio. Point an MCP client (e.g. Claude Code) at this binary and it exposes three tools:

  get            read a TOML section by bracket path
  list_sections  list every section in a file
  upsert         create or update a section, validated against .ta/config.toml

Schemas are resolved by walking up from the target file for a .ta/config.toml, then falling back to ~/.ta/config.toml. The ta binary reads no flags beyond those listed below at runtime; all tool arguments arrive via MCP.`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		showVersion = fs.Bool("version", false, "print version and exit")
		showHelp    = fs.Bool("help", false, "print usage and exit")
		logStartup  = fs.Bool("log-startup", false, "log a startup banner to stderr before serving")
	)
	fs.Usage = func() { renderHelp(stdout, fs) }

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			renderHelp(stdout, fs)
			return 0
		}
		renderError(stderr, fmt.Sprintf("parse flags: %v", err))
		return 2
	}

	if *showHelp {
		renderHelp(stdout, fs)
		return 0
	}
	if *showVersion {
		renderVersion(stdout)
		return 0
	}

	srv, err := mcpsrv.New(mcpsrv.Config{Name: appName, Version: version()})
	if err != nil {
		renderError(stderr, err.Error())
		return 1
	}

	if *logStartup {
		renderStartup(stderr)
	}

	if err := srv.Run(context.Background()); err != nil {
		renderError(stderr, err.Error())
		return 1
	}
	return 0
}

func renderHelp(w io.Writer, fs *flag.FlagSet) {
	p := laslig.New(w, humanPolicy())
	_ = p.Section(fmt.Sprintf("%s %s", appName, version()))
	_ = p.Paragraph(laslig.Paragraph{Body: helpBody})
	pairs := []laslig.Field{}
	fs.VisitAll(func(f *flag.Flag) {
		pairs = append(pairs, laslig.Field{
			Label: "--" + f.Name,
			Value: f.Usage,
		})
	})
	_ = p.KV(laslig.KV{Title: "Flags", Pairs: pairs})
}

func renderVersion(w io.Writer) {
	p := laslig.New(w, humanPolicy())
	info, _ := debug.ReadBuildInfo()
	pairs := []laslig.Field{
		{Label: "name", Value: appName},
		{Label: "version", Value: version()},
	}
	if info != nil {
		if rev := vcsSetting(info, "vcs.revision"); rev != "" {
			pairs = append(pairs, laslig.Field{Label: "commit", Value: rev, Identifier: true})
		}
		if modified := vcsSetting(info, "vcs.modified"); modified != "" {
			pairs = append(pairs, laslig.Field{Label: "modified", Value: modified, Muted: true})
		}
		pairs = append(pairs, laslig.Field{Label: "go", Value: info.GoVersion, Muted: true})
	}
	_ = p.KV(laslig.KV{Title: appName, Pairs: pairs})
}

func renderStartup(w io.Writer) {
	p := laslig.New(w, humanPolicy())
	_ = p.Notice(laslig.Notice{
		Level: laslig.NoticeInfoLevel,
		Title: fmt.Sprintf("%s %s ready", appName, version()),
		Body:  "serving MCP over stdio",
	})
}

func renderError(w io.Writer, msg string) {
	p := laslig.New(w, humanPolicy())
	_ = p.Notice(laslig.Notice{
		Level: laslig.NoticeErrorLevel,
		Title: appName,
		Body:  msg,
	})
}

func humanPolicy() laslig.Policy {
	return laslig.Policy{Format: laslig.FormatAuto, Style: laslig.StyleAuto}
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

func vcsSetting(info *debug.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}
