package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newServeCmd registers `ta serve` — the HTTP cascade browser
// subcommand. It is distinct from bare-`ta` MCP serve behavior (see
// runRoot / runServe in main.go), which speaks stdio MCP to clients.
//
// L2-A scope: cobra wiring + flag plumbing only. RunE constructs a
// serveConfig and delegates to runServeHTTP; L2-B owns the
// internal/server package that consumes the config.
//
// Defaults: --bind = 127.0.0.1, --port = 4321 (per L2-A round-2 fix
// pinning the default contract so L2-E docs alignment is verifiable).
func newServeCmd() *cobra.Command {
	var (
		bind string
		port int
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP cascade browser server",
		Long: "Serve live .ta/ cascade records as HTML via Track A templates. " +
			"Distinct from bare-`ta` MCP serve (stdio). --bind / --port control " +
			"the HTTP listener.",
		Example: `  ta serve
  ta serve --bind 0.0.0.0 --port 8080`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg := serveConfig{Bind: bind, Port: port}
			return runServeHTTPFunc(c.Context(), c.ErrOrStderr(), cfg)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "HTTP listener bind address")
	cmd.Flags().IntVar(&port, "port", 4321, "HTTP listener port")
	return cmd
}

// serveConfig is the L2-A → L2-B delegation contract. L2-B's
// internal/server package will consume this shape via its own
// constructor; cmd/ta does not import internal/server here so this
// droplet stays atomic to cmd/ta only.
type serveConfig struct {
	Bind string
	Port int
}

// runServeHTTPFunc is the L2-A → L2-B handoff seam. Tests can swap
// this to capture the serveConfig that RunE would have passed; L2-B
// builders will retarget it (or replace the body of runServeHTTP) to
// invoke the real server.New(cfg).Run(ctx) once the internal/server
// package exists.
var runServeHTTPFunc = runServeHTTP

// runServeHTTP is the placeholder L2-A → L2-B handoff body. While
// L2-B's internal/server package is in flight, this returns nil after
// a stderr notice so the cobra command tree is testable without the
// server package existing yet.
func runServeHTTP(_ context.Context, stderr io.Writer, cfg serveConfig) error {
	fmt.Fprintf(stderr, "ta serve: pending L2-B server implementation (bind=%s port=%d)\n", cfg.Bind, cfg.Port)
	return nil
}
