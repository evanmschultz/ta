package install_hygiene

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
)

// F8RunningMCPNotice is the user-facing hint emitted by Run after a
// successful install when a running ta MCP server is detected. The text
// is exported so tests can pin the exact contract.
const F8RunningMCPNotice = "NOTICE: running ta MCP server detected; restart Claude Code or Codex to pick up the new install"

// execCommand is the seam tests use to stub pgrep invocations. It
// defaults to exec.CommandContext so production code shells out for
// real; tests override it to inject ErrNotFound, exit-1, or controlled
// stdout without depending on the host's pgrep / process tree.
var execCommand = exec.CommandContext

// detectRunningTaMCP returns true when at least one process whose
// argv[0] is exactly `ta` is running on the host. It shells out to
// `pgrep -f '^ta$'` and interprets the result:
//
//   - pgrep absent (exec.ErrNotFound OR *exec.Error wrapping
//     fs.ErrNotExist OR fs.ErrNotExist directly) → return false, nil.
//     This is the portability fallback: Windows has no pgrep; stripped
//     Linux containers may also lack it.
//   - pgrep exits 1 (no match) → return false, nil. This is pgrep's
//     documented "nothing matched" signal, not a real error.
//   - pgrep exits 0 with non-empty stdout → return true, nil.
//   - Any other error (e.g. signal, exit code >1, I/O failure) →
//     return false, err so callers can decide whether to surface it.
//
// The hint Run emits on detection is best-effort: a failed detection
// must never block the install itself.
func detectRunningTaMCP(ctx context.Context) (bool, error) {
	cmd := execCommand(ctx, "pgrep", "-f", "^ta$")
	out, err := cmd.Output()
	if err == nil {
		return len(out) > 0, nil
	}

	// Portability fallback — treat "pgrep is not installed" as a
	// no-op false rather than a hard failure.
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, fs.ErrNotExist) {
		return false, nil
	}

	// pgrep exit-1 means "no match" — by convention not an error.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}

	return false, err
}
