package dotta

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExpandTilde_Empty checks the empty-string passthrough branch:
// callers commonly pass through unset config values, so empty in →
// empty out (no error, no $HOME lookup).
func TestExpandTilde_Empty(t *testing.T) {
	got, err := ExpandTilde("")
	if err != nil {
		t.Fatalf("ExpandTilde(\"\") returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("ExpandTilde(\"\") = %q, want \"\"", got)
	}
}

// TestExpandTilde_BareTilde checks that a single `~` expands to the
// user's home directory (no trailing slash, no extra path elements).
func TestExpandTilde_BareTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() failed: %v", err)
	}
	got, err := ExpandTilde("~")
	if err != nil {
		t.Fatalf("ExpandTilde(\"~\") returned error: %v", err)
	}
	if got != home {
		t.Fatalf("ExpandTilde(\"~\") = %q, want %q", got, home)
	}
}

// TestExpandTilde_TildeSlash checks the `~/...` branch — the most
// common shape users will pass through a JSON/TOML config value.
func TestExpandTilde_TildeSlash(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() failed: %v", err)
	}
	got, err := ExpandTilde("~/foo")
	if err != nil {
		t.Fatalf("ExpandTilde(\"~/foo\") returned error: %v", err)
	}
	want := filepath.Join(home, "foo")
	if got != want {
		t.Fatalf("ExpandTilde(\"~/foo\") = %q, want %q", got, want)
	}
}

// TestExpandTilde_NoTilde checks that an already-absolute path passes
// through unchanged. The function must not "normalize" or otherwise
// rewrite paths that don't begin with `~`.
func TestExpandTilde_NoTilde(t *testing.T) {
	const in = "/abs/path"
	got, err := ExpandTilde(in)
	if err != nil {
		t.Fatalf("ExpandTilde(%q) returned error: %v", in, err)
	}
	if got != in {
		t.Fatalf("ExpandTilde(%q) = %q, want %q", in, got, in)
	}
}
