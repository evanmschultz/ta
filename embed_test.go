// Tests for the binary-embedded examples tree exposed via
// EmbeddedExamples(). Package `ta` is the root mount point; these tests
// pin that the //go:embed all:examples directive picks up every
// substrate the install engine (and drop_010 demo cleanup) expects to
// find rooted at examples/.

package ta_test

import (
	"io/fs"
	"testing"

	ta "github.com/evanmschultz/ta"
)

// TestEmbeddedExamples_NonEmpty pins that //go:embed all:examples
// resolved successfully at compile time. A drift here (e.g. directive
// stripped from embed.go) would compile but produce an empty fs.FS;
// this test fails fast in that scenario.
func TestEmbeddedExamples_NonEmpty(t *testing.T) {
	got := ta.EmbeddedExamples()
	if got == nil {
		t.Fatal("EmbeddedExamples() returned nil fs.FS")
	}
	entries, err := fs.ReadDir(got, "examples")
	if err != nil {
		t.Fatalf("ReadDir(examples): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("examples/ tree is empty — //go:embed all:examples did not pick up any files")
	}
}

// TestEmbeddedExamples_ThariqStandalone pins drop_010 L2-A: the thariq
// standalone Astro project lives at examples/thariq/ with its own
// package.json + the 5 routed pages. Substrate example_thariq's source
// path resolves through this embed surface.
func TestEmbeddedExamples_ThariqStandalone(t *testing.T) {
	embedded := ta.EmbeddedExamples()
	for _, path := range []string{
		"examples/thariq/package.json",
		"examples/thariq/astro.config.mjs",
		"examples/thariq/src/pages/index.astro",
		"examples/thariq/src/pages/drop-dashboard.astro",
		"examples/thariq/src/pages/droplet-kanban.astro",
		"examples/thariq/src/pages/planner-detail.astro",
		"examples/thariq/src/pages/qa-twins.astro",
		"examples/thariq/src/mock/thariq_records.ts",
	} {
		if _, err := fs.Stat(embedded, path); err != nil {
			t.Errorf("missing embedded thariq file %q: %v", path, err)
		}
	}
}

// TestEmbeddedExamples_StilStandalone pins drop_010 L2-B: the stil
// standalone Astro project lives at examples/stil/ with its own
// package.json + the 7 substantive cascade-view routes + index +
// StilLayout + stil_records mock. Substrate example_stil's source path
// resolves through this embed surface.
func TestEmbeddedExamples_StilStandalone(t *testing.T) {
	embedded := ta.EmbeddedExamples()
	for _, path := range []string{
		"examples/stil/package.json",
		"examples/stil/astro.config.mjs",
		"examples/stil/src/pages/index.astro",
		"examples/stil/src/pages/cascade_drop.astro",
		"examples/stil/src/pages/cascade_droplet.astro",
		"examples/stil/src/pages/cascade_planner.astro",
		"examples/stil/src/pages/cascade_qa_proof.astro",
		"examples/stil/src/pages/cascade_qa_falsification.astro",
		"examples/stil/src/pages/roadmap_version.astro",
		"examples/stil/src/pages/schema_browser.astro",
		"examples/stil/src/layouts/StilLayout.astro",
		"examples/stil/src/mock/stil_records.ts",
	} {
		if _, err := fs.Stat(embedded, path); err != nil {
			t.Errorf("missing embedded stil file %q: %v", path, err)
		}
	}
}

// TestEmbeddedExamples_LegacyCategoriesIntact pins that the existing
// 4 install-substrate-feeding subtrees (agents/, configs/,
// docs-templates/, schemas/) remain present after drop_010's pivot —
// adding thariq + stil siblings must not displace any prior content.
func TestEmbeddedExamples_LegacyCategoriesIntact(t *testing.T) {
	embedded := ta.EmbeddedExamples()
	for _, dir := range []string{
		"examples/agents",
		"examples/configs",
		"examples/docs-templates",
		"examples/schemas",
	} {
		info, err := fs.Stat(embedded, dir)
		if err != nil {
			t.Errorf("legacy embed category %q missing: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("legacy embed entry %q is not a directory", dir)
		}
	}
}
