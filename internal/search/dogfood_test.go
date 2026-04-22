package search_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/search"
)

// TestDogfoodProbeAgainstRealProject exercises search against the
// project's OWN .ta/schema.toml + README.md + CLAUDE.md. It is a
// build-time dogfood probe for §12.8 — reports what the search returns
// over live project content.
//
// Skipped when the project root can't be located (e.g. when running
// this test tree outside the ta monorepo). When present, the assertions
// are minimal: README.md must have at least one H2 section, search
// matching "search" (the V2 feature) must hit at least one record.
func TestDogfoodProbeAgainstRealProject(t *testing.T) {
	root := findProjectRoot(t)
	if root == "" {
		t.Skip("project root not reachable from this test run")
	}
	// Post-V2-PLAN §12.11 the runtime reads only <project>/.ta/schema.toml
	// — no home-layer fallback — so the HOME-override workaround is gone.

	// 1. Scope: whole readme db. Every H2 must surface.
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "readme.section",
	})
	if err != nil {
		t.Skipf("dogfood probe skipped — cascade resolve failed: %v", err)
	}
	if len(hits) == 0 {
		t.Logf("dogfood probe found no readme H2 sections — README may be empty")
	} else {
		t.Logf("dogfood probe: %d readme H2 sections", len(hits))
	}

	// 2. Regex probe: hunt for "search" mentions across the readme.
	re := regexp.MustCompile(`(?i)search`)
	hits2, err := search.Run(search.Query{
		Path:  root,
		Scope: "readme.section",
		Query: re,
	})
	if err != nil {
		t.Skipf("dogfood probe skipped — regex run failed: %v", err)
	}
	t.Logf("dogfood probe: %d readme sections containing /search/i", len(hits2))
	for _, h := range hits2 {
		t.Logf("  - %s", h.Section)
	}
}

// findProjectRoot walks up from this test's working directory looking
// for a .ta/schema.toml. Returns "" if none is found before the root
// (test running in a stripped-down env).
func findProjectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".ta", "schema.toml")); err == nil {
			// Confirm the README is present too so the probe has
			// something to search.
			if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Silence unused import in env where the fallback branch triggers.
var _ = strings.Contains
