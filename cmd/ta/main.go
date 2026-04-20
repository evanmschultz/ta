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

	"github.com/evanmschultz/ta/internal/mcpsrv"
)

const appName = "ta"

const helpBody = `ta speaks MCP over stdio. Point an MCP client (e.g. Claude Code) at this binary
and it exposes three tools:

  get            read a TOML section by bracket path
  list_sections  list every section in a file
  upsert         create or update a section, validated against .ta/config.toml

Schemas resolve by walking up from the target file for a .ta/config.toml,
then falling back to ~/.ta/config.toml. All tool arguments arrive via MCP;
the binary itself reads only the flags listed below.`

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
	fs.Usage = func() { writeHelp(stdout, fs) }

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeHelp(stdout, fs)
			return 0
		}
		fmt.Fprintf(stderr, "ta: parse flags: %v\n", err)
		return 2
	}

	if *showHelp {
		writeHelp(stdout, fs)
		return 0
	}
	if *showVersion {
		writeVersion(stdout)
		return 0
	}

	srv, err := mcpsrv.New(mcpsrv.Config{Name: appName, Version: version()})
	if err != nil {
		fmt.Fprintf(stderr, "ta: %v\n", err)
		return 1
	}

	if *logStartup {
		fmt.Fprintf(stderr, "ta %s: serving MCP over stdio\n", version())
	}

	if err := srv.Run(context.Background()); err != nil {
		fmt.Fprintf(stderr, "ta: %v\n", err)
		return 1
	}
	return 0
}

func writeHelp(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, "%s %s\n\n%s\n\nFlags:\n", appName, version(), helpBody)
	fs.VisitAll(func(f *flag.Flag) {
		fmt.Fprintf(w, "  --%-12s %s\n", f.Name, f.Usage)
	})
}

func writeVersion(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n", appName, version())
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if rev := vcsSetting(info, "vcs.revision"); rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		fmt.Fprintf(w, "commit %s\n", rev)
	}
	if modified := vcsSetting(info, "vcs.modified"); modified == "true" {
		fmt.Fprintln(w, "modified")
	}
	fmt.Fprintf(w, "go %s\n", info.GoVersion)
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
