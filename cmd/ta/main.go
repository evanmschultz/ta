// Command ta runs the ta MCP server, exposing TOML files to MCP clients as
// schema-validated, agent-accessible data.
//
// See docs/ta.md for the design and docs/PLAN.md for the MVP build plan.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	_ "github.com/evanmschultz/laslig" // anchored for Phase 7 rendering

	"github.com/evanmschultz/ta/internal/mcpsrv"
)

const appName = "ta"

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		printVersion()
		return
	}

	srv, err := mcpsrv.New(mcpsrv.Config{
		Name:    appName,
		Version: version(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ta: %v\n", err)
		os.Exit(1)
	}
	if err := srv.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "ta: %v\n", err)
		os.Exit(1)
	}
}

func printVersion() {
	fmt.Printf("%s %s\n", appName, version())
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown)"
	}
	if info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
