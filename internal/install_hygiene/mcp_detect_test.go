package install_hygiene

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/render"
)

// withExecCommand swaps the package-level execCommand seam for the
// duration of a test. Cleanup restores the previous value so parallel
// tests stay isolated.
//
// Tests MUST NOT call t.Parallel() when using this helper — the seam is
// a single package-level var and concurrent swaps would race. The helper
// is therefore deliberately incompatible with parallel scheduling.
func withExecCommand(t *testing.T, fn func(ctx context.Context, name string, args ...string) *exec.Cmd) {
	t.Helper()
	prev := execCommand
	execCommand = fn
	t.Cleanup(func() { execCommand = prev })
}

// TestF8_DetectRunningTaMCP_PgrepAbsentReturnsFalse — when pgrep is not
// installed (the helper stubs the exec seam to invoke a non-existent
// binary), detectRunningTaMCP returns (false, nil). The install must
// not fail on hosts that lack pgrep (Windows, stripped Linux).
func TestF8_DetectRunningTaMCP_PgrepAbsentReturnsFalse(t *testing.T) {
	withExecCommand(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		// Bare name with no slash; LookPath will fail with
		// exec.ErrNotFound when the binary is not on PATH.
		return exec.CommandContext(ctx, "ta-test-no-such-binary-xyz-9f7a")
	})

	got, err := detectRunningTaMCP(context.Background())
	if err != nil {
		t.Fatalf("detectRunningTaMCP returned error %v; want nil", err)
	}
	if got {
		t.Errorf("detectRunningTaMCP = true; want false when pgrep absent")
	}
}

// TestF8_DetectRunningTaMCP_NoMatchReturnsFalse — when pgrep runs and
// exits 1 (no match), detectRunningTaMCP returns (false, nil). exit-1
// is pgrep's documented "nothing matched" signal, not a real error.
func TestF8_DetectRunningTaMCP_NoMatchReturnsFalse(t *testing.T) {
	withExecCommand(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		// `false` is a tiny POSIX utility that always exits 1.
		return exec.CommandContext(ctx, "false")
	})

	got, err := detectRunningTaMCP(context.Background())
	if err != nil {
		t.Fatalf("detectRunningTaMCP returned error %v; want nil", err)
	}
	if got {
		t.Errorf("detectRunningTaMCP = true; want false on exit-1 no-match")
	}
}

// TestF8_DetectRunningTaMCP_MatchReturnsTrue — when pgrep exits 0 with
// non-empty stdout, detectRunningTaMCP returns (true, nil). Pinned so
// the hint-emission path in Run actually fires when expected.
func TestF8_DetectRunningTaMCP_MatchReturnsTrue(t *testing.T) {
	withExecCommand(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		// `echo` writes its args + a trailing newline and exits 0 —
		// non-empty stdout is the trigger.
		return exec.CommandContext(ctx, "echo", "12345")
	})

	got, err := detectRunningTaMCP(context.Background())
	if err != nil {
		t.Fatalf("detectRunningTaMCP returned error %v; want nil", err)
	}
	if !got {
		t.Errorf("detectRunningTaMCP = false; want true on exit-0 + non-empty stdout")
	}
}

// TestF8_Install_EmitsHintWhenDetectedTrue — when Run completes and
// detectRunningTaMCP returns true, Report.Notices contains the F8 hint
// string. This is the user-facing wiring: install summary surfaces the
// "restart Claude Code / Codex" guidance when a stale MCP server is
// likely still loaded.
func TestF8_Install_EmitsHintWhenDetectedTrue(t *testing.T) {
	withExecCommand(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		// Force a positive detection — exit 0, non-empty stdout.
		return exec.CommandContext(ctx, "echo", "67890")
	})

	home := t.TempDir()
	var buf bytes.Buffer
	rr := render.New(&buf)

	rep, err := Run(context.Background(), Options{
		Home:     home,
		Renderer: rr,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var found bool
	for _, n := range rep.Notices {
		if strings.Contains(n, "running ta MCP server detected") &&
			strings.Contains(n, "restart Claude Code or Codex") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Report.Notices missing F8 hint; got %#v", rep.Notices)
	}

	// Sanity: the hint exact string is stable so callers can pin it.
	if rep.Notices[0] != F8RunningMCPNotice {
		t.Errorf("Report.Notices[0] = %q, want %q", rep.Notices[0], F8RunningMCPNotice)
	}
}

// TestF8_Install_NoHintWhenDetectedFalse — guards the negative path:
// when detection returns false, Report.Notices does NOT carry the F8
// hint. Prevents the hint from leaking onto fresh hosts with no running
// MCP server.
func TestF8_Install_NoHintWhenDetectedFalse(t *testing.T) {
	withExecCommand(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		// `false` → exit 1 → detectRunningTaMCP returns false.
		return exec.CommandContext(ctx, "false")
	})

	home := t.TempDir()
	var buf bytes.Buffer
	rr := render.New(&buf)

	rep, err := Run(context.Background(), Options{
		Home:     home,
		Renderer: rr,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	for _, n := range rep.Notices {
		if strings.Contains(n, "running ta MCP server detected") {
			t.Errorf("Report.Notices unexpectedly carries F8 hint: %q", n)
		}
	}
}

// Compile-time sanity: ensure os.Stderr is still referenced from the
// package so the test file's imports don't drift if the production
// code's import set changes. (Static check — never executed.)
var _ = os.Stderr
