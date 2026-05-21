package main

import (
	"context"
	"io"
	"testing"
)

// TestServeCmd_RegistersSubcommand pins that `ta serve` is reachable
// via the root command tree. Regression guard for L2-A a2 (registration
// in newRootCmd).
func TestServeCmd_RegistersSubcommand(t *testing.T) {
	root := newRootCmd()
	target, _, err := root.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("root.Find([\"serve\"]): %v", err)
	}
	if target == nil {
		t.Fatalf("newRootCmd does not register a 'serve' subcommand")
	}
	if target.Use != "serve" {
		t.Fatalf("found subcommand Use=%q via Find(\"serve\"), want \"serve\"", target.Use)
	}
}

// TestServeCmd_DefaultBindAndPort pins the L2-A round-2 default
// contract: --bind defaults to 127.0.0.1 and --port defaults to 4321.
// L2-E docs alignment depends on these defaults being stable.
func TestServeCmd_DefaultBindAndPort(t *testing.T) {
	cmd := newServeCmd()
	bindFlag := cmd.Flags().Lookup("bind")
	if bindFlag == nil {
		t.Fatalf("newServeCmd missing --bind flag")
	}
	if got, want := bindFlag.DefValue, "127.0.0.1"; got != want {
		t.Fatalf("--bind default = %q, want %q", got, want)
	}
	portFlag := cmd.Flags().Lookup("port")
	if portFlag == nil {
		t.Fatalf("newServeCmd missing --port flag")
	}
	if got, want := portFlag.DefValue, "4321"; got != want {
		t.Fatalf("--port default = %q, want %q", got, want)
	}
}

// TestServeCmd_BindAndPortFlagsDelegate verifies that --bind and
// --port flag values flow into the serveConfig struct that gets
// passed to the L2-B server handoff. Uses runServeHTTPFunc swap to
// capture cfg without booting the placeholder runServeHTTP body.
func TestServeCmd_BindAndPortFlagsDelegate(t *testing.T) {
	orig := runServeHTTPFunc
	t.Cleanup(func() { runServeHTTPFunc = orig })

	var captured serveConfig
	runServeHTTPFunc = func(_ context.Context, _ io.Writer, cfg serveConfig) error {
		captured = cfg
		return nil
	}

	cmd := newServeCmd()
	cmd.SetArgs([]string{"--bind", "0.0.0.0", "--port", "8080"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute: %v", err)
	}
	if got, want := captured.Bind, "0.0.0.0"; got != want {
		t.Fatalf("captured.Bind = %q, want %q", got, want)
	}
	if got, want := captured.Port, 8080; got != want {
		t.Fatalf("captured.Port = %d, want %d", got, want)
	}
}
