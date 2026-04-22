package md

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/record"
)

// TestDogfoodProbeReadme is a diagnostic test that verifies the MD
// backend can enumerate declared sections in the project's real
// README.md against the dogfood schema (H1 "title" + H2 "section").
// It is opportunistic — if the file is not found (e.g. running from an
// isolated module cache), the test is skipped rather than failing.
//
// The probe confirms that the scanner handles real-world content
// (fenced code, tables, lists, nested emphasis) without false-positive
// heading detection AND that the schema-driven filter returns the
// expected declared-level slugs.
func TestDogfoodProbeReadme(t *testing.T) {
	root, ok := findProjectRoot()
	if !ok {
		t.Skip("project root not found")
	}
	path := filepath.Join(root, "README.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no README.md at %s: %v", path, err)
	}

	// Use the real dogfood declared types (per .ta/schema.toml): H1
	// readme.title, H2 readme.section.
	types := []record.DeclaredType{
		{Name: "title", Heading: 1},
		{Name: "section", Heading: 2},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}

	addrs, err := b.List(data, "")
	if err != nil {
		t.Fatalf("List(README.md): %v", err)
	}
	if len(addrs) == 0 {
		t.Fatalf("expected at least one declared section in README.md; got 0")
	}

	// Log each address so the probe output is visible via `go test -v`.
	for _, a := range addrs {
		t.Logf("address: %s", a)
	}

	// Assert the expected dogfood shape: one title "ta" plus at least
	// one section.
	if addrs[0] != "title.ta" {
		t.Errorf("first declared address = %q, want title.ta", addrs[0])
	}
	sectionCount := 0
	for _, a := range addrs {
		if len(a) > len("section.") && a[:len("section.")] == "section." {
			sectionCount++
		}
	}
	if sectionCount == 0 {
		t.Error("expected at least one section.* address in README.md")
	}

	// Expected H2 slugs present in the current README (at the time
	// this probe was written). Each expected slug must appear; any
	// additional slugs beyond this set are fine (README grows).
	wantSections := []string{
		"section.install",
		"section.mcp-client-config",
		"section.schemas",
		"section.building-from-source",
		"section.license",
	}
	have := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		have[a] = true
	}
	for _, want := range wantSections {
		if !have[want] {
			t.Errorf("expected declared address %q missing from List output %v", want, addrs)
		}
	}
}
