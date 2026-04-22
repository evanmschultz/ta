package md

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDogfoodProbeReadme is a diagnostic test that verifies the MD
// backend can enumerate sections in the project's real README.md. It
// is opportunistic — if the file is not found (e.g. running from an
// isolated module cache), the test is skipped rather than failing.
//
// The probe confirms that the scanner handles real-world content
// (fenced code, tables, lists, nested emphasis) without false-positive
// heading detection.
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

	hs, err := scanATX(data)
	if err != nil {
		t.Fatalf("scanATX(README.md): %v", err)
	}
	if len(hs) == 0 {
		t.Fatalf("expected at least one heading in README.md; got 0")
	}

	// Log each heading so the probe output is visible via `go test -v`.
	for _, h := range hs {
		t.Logf("H%d slug=%q text=%q lines=%d:%d", h.Level, h.Slug, h.Text, h.LineStart, h.LineEnd)
	}

	// Assert the expected dogfood shape: one H1 "ta" plus at least
	// one H2 (the README always has Install / License at minimum).
	if hs[0].Level != 1 {
		t.Errorf("first heading should be H1, got H%d", hs[0].Level)
	}
	if hs[0].Slug != "ta" {
		t.Errorf("first heading slug = %q, want %q", hs[0].Slug, "ta")
	}
	h2Count := 0
	for _, h := range hs {
		if h.Level == 2 {
			h2Count++
		}
	}
	if h2Count == 0 {
		t.Error("expected at least one H2 heading in README.md")
	}

	// Exercise Backend.List at level 1 and level 2.
	b1, _ := NewBackend(1)
	titles, err := b1.List(data, "readme.title")
	if err != nil {
		t.Errorf("List(level=1): %v", err)
	}
	if len(titles) == 0 {
		t.Error("no H1 titles surfaced via List")
	}
	t.Logf("readme.title addresses: %v", titles)

	b2, _ := NewBackend(2)
	sections, err := b2.List(data, "readme.section")
	if err != nil {
		t.Errorf("List(level=2): %v", err)
	}
	if len(sections) == 0 {
		t.Error("no H2 sections surfaced via List")
	}
	t.Logf("readme.section addresses: %v", sections)

	// Expected H2 slugs present in the current README (at the time
	// this probe was written). Each expected slug must appear; any
	// additional slugs beyond this set are fine (README grows).
	wantH2 := []string{
		"readme.section.install",
		"readme.section.mcp-client-config",
		"readme.section.schemas",
		"readme.section.building-from-source",
		"readme.section.license",
	}
	have := make(map[string]bool, len(sections))
	for _, s := range sections {
		have[s] = true
	}
	for _, want := range wantH2 {
		if !have[want] {
			t.Errorf("expected H2 slug %q missing from List output %v", want, sections)
		}
	}
}
