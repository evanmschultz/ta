package dotta

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeMapping is a small fixture helper that writes a mapping.toml
// with the given body inside a fresh t.TempDir subtree directory and
// returns the directory path. Tests that need a specific file state
// (missing, unreadable) operate on the returned dir directly.
func writeMapping(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MappingFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("seed mapping.toml: %v", err)
	}
	return dir
}

func TestLoadMapping_Absent(t *testing.T) {
	dir := t.TempDir() // no mapping.toml written
	got, err := LoadMapping(dir)
	if err != nil {
		t.Fatalf("LoadMapping(absent) error = %v, want nil", err)
	}
	if got != (Mapping{}) {
		t.Fatalf("LoadMapping(absent) = %+v, want zero-value Mapping", got)
	}
}

func TestLoadMapping_FullValid(t *testing.T) {
	body := `destination = "~/.claude"
on_conflict = "overwrite"
`
	dir := writeMapping(t, body)
	got, err := LoadMapping(dir)
	if err != nil {
		t.Fatalf("LoadMapping error = %v, want nil", err)
	}
	want := Mapping{Destination: "~/.claude", OnConflict: OnConflictOverwrite}
	if got != want {
		t.Fatalf("LoadMapping = %+v, want %+v", got, want)
	}
}

func TestLoadMapping_DestinationOnly(t *testing.T) {
	body := `destination = "~/.claude"
`
	dir := writeMapping(t, body)
	got, err := LoadMapping(dir)
	if err != nil {
		t.Fatalf("LoadMapping error = %v, want nil", err)
	}
	if got.Destination != "~/.claude" {
		t.Fatalf("Destination = %q, want %q", got.Destination, "~/.claude")
	}
	if got.OnConflict != "" {
		t.Fatalf("OnConflict = %q, want empty (caller-defaulted)", got.OnConflict)
	}
}

func TestLoadMapping_BadOnConflict(t *testing.T) {
	body := `destination = "~/.claude"
on_conflict = "wreck"
`
	dir := writeMapping(t, body)
	_, err := LoadMapping(dir)
	if err == nil {
		t.Fatalf("LoadMapping(bad on_conflict) error = nil, want non-nil")
	}
	msg := err.Error()
	for _, want := range []string{"dotta:", `"wreck"`, "want skip|overwrite|merge|prompt"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing substring %q", msg, want)
		}
	}
}

func TestLoadMapping_MalformedTOML(t *testing.T) {
	// `destination = ` with no value is a syntax error; the v2 decoder
	// reports it during Unmarshal before any field validation can run.
	body := "destination = \n[broken"
	dir := writeMapping(t, body)
	_, err := LoadMapping(dir)
	if err == nil {
		t.Fatalf("LoadMapping(malformed) error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "dotta: parse mapping at") {
		t.Fatalf("error %q missing prefix %q", err.Error(), "dotta: parse mapping at")
	}
}

func TestLoadMapping_UnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based unreadable simulation does not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses POSIX permission bits; cannot simulate unreadable file")
	}
	dir := writeMapping(t, `destination = "~/.claude"`+"\n")
	path := filepath.Join(dir, MappingFilename)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod 0: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0o644) // restore so t.TempDir cleanup can remove it
	})

	_, err := LoadMapping(dir)
	if err == nil {
		t.Fatalf("LoadMapping(unreadable) error = nil, want read error")
	}
	if !strings.Contains(err.Error(), "dotta: read mapping at") {
		t.Fatalf("error %q missing prefix %q", err.Error(), "dotta: read mapping at")
	}
	// Defensive: confirm the wrapped sentinel is NOT fs.ErrNotExist —
	// otherwise the function would have returned the zero-value clean
	// path instead of the read-error path.
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unreadable error unexpectedly wraps fs.ErrNotExist: %v", err)
	}
}
