// drop_004 H2 cmd/ta coverage drive. resolveCLIPath is the
// single helper every path-taking CLI command goes through; pre-H2
// it sat at 75% with the absolute-input and Abs-error branches
// uncovered. These tests pin the contract from path.go's docstring
// by construction.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// pathCmdWithFlag returns a fresh cobra.Command with addPathFlag
// applied. The returned command's Flags().Set("path", v) drives the
// resolver under test.
func pathCmdWithFlag(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "x"}
	addPathFlag(cmd)
	return cmd
}

// TestResolveCLIPath_EmptyFallsBackToCwd asserts that an empty flag
// value resolves to the current working directory. We can't easily
// pin the absolute path of cwd in CI, so we compare against
// os.Getwd directly.
func TestResolveCLIPath_EmptyFallsBackToCwd(t *testing.T) {
	t.Parallel()
	cmd := pathCmdWithFlag(t)
	// Empty (default) value should produce os.Getwd.
	wantCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	got, err := resolveCLIPath(cmd)
	if err != nil {
		t.Fatalf("resolveCLIPath: %v", err)
	}
	if got != wantCwd {
		t.Errorf("got %q, want %q", got, wantCwd)
	}
}

// TestResolveCLIPath_AbsolutePathPassesThrough asserts that an
// already-absolute value passes through filepath.Clean unchanged.
func TestResolveCLIPath_AbsolutePathPassesThrough(t *testing.T) {
	t.Parallel()
	cmd := pathCmdWithFlag(t)
	tmp := t.TempDir()
	// Append an artificial trailing slash + duplicate sep that
	// Clean will normalise.
	dirty := tmp + string(os.PathSeparator) + "."
	if err := cmd.Flags().Set("path", dirty); err != nil {
		t.Fatalf("Flags().Set: %v", err)
	}
	got, err := resolveCLIPath(cmd)
	if err != nil {
		t.Fatalf("resolveCLIPath: %v", err)
	}
	want := filepath.Clean(tmp)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolveCLIPath_RelativeResolved asserts that a relative value
// is run through filepath.Abs so callers downstream receive an
// absolute path.
func TestResolveCLIPath_RelativeResolved(t *testing.T) {
	t.Parallel()
	cmd := pathCmdWithFlag(t)
	if err := cmd.Flags().Set("path", "./relative-here"); err != nil {
		t.Fatalf("Flags().Set: %v", err)
	}
	got, err := resolveCLIPath(cmd)
	if err != nil {
		t.Fatalf("resolveCLIPath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	if !strings.HasSuffix(got, "relative-here") {
		t.Errorf("expected suffix 'relative-here', got %q", got)
	}
}

// TestResolveCLIPath_FlagMissing covers the GetString error path.
// We construct a cobra.Command without calling addPathFlag; the
// lookup must surface a wrapped error rather than panic.
func TestResolveCLIPath_FlagMissing(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "noflag"}
	// No addPathFlag — GetString("path") will return an error.
	_, err := resolveCLIPath(cmd)
	if err == nil {
		t.Fatalf("expected error when --path flag absent, got nil")
	}
	if !strings.Contains(err.Error(), "resolve --path flag") {
		t.Errorf("error message missing wrap context: %v", err)
	}
}

// TestAddPathFlag_RegistersStringFlag asserts addPathFlag attaches a
// string flag named pathFlagName with an empty default. Pins the
// contract every path-taking command depends on.
func TestAddPathFlag_RegistersStringFlag(t *testing.T) {
	t.Parallel()
	cmd := pathCmdWithFlag(t)
	f := cmd.Flags().Lookup(pathFlagName)
	if f == nil {
		t.Fatalf("expected --%s flag registered, got nil", pathFlagName)
	}
	if f.Value.Type() != "string" {
		t.Errorf("flag type = %q, want string", f.Value.Type())
	}
	if f.DefValue != "" {
		t.Errorf("flag default = %q, want empty", f.DefValue)
	}
}
