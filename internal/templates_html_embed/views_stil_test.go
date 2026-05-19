package templates_html_embed

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// stilViewNames lists the seven L3-E3 stil view route stems whose
// pre-rendered HTML lives under dist/stil/<view>/index.html in the
// embedded FS. The list is the authoritative set for the per-view
// golden compares (TestTemplatesHtmlEmbed_Stil_<view>) and for the two
// invariant sweeps (TokensPresent, ZeroJS) that fan out across all
// seven dist files.
//
// Order matches docs/cascade-methodology.md §5.3 enumeration: cascade
// twins first (drop → planner → droplet → qa_proof → qa_falsification),
// then roadmap_version, then schema_browser.
var stilViewNames = []string{
	"cascade_drop",
	"cascade_planner",
	"cascade_droplet",
	"cascade_qa_proof",
	"cascade_qa_falsification",
	"roadmap_version",
	"schema_browser",
}

// TestTemplatesHtmlEmbed_Stil drives all seven L3-E3 stil view dist
// HTML files through a byte-for-byte golden compare. Each sub-test
// reads dist/stil/<view>/index.html from the embedded FS (the same FS
// the runtime serves) and compares against
// testdata/stil/<view>.html.golden.
//
// A drift between the embedded dist HTML and the committed golden
// signals one of two changes:
//
//  1. The authored stil view source under web/templates_embed/src/
//     pages/stil/<view>.astro (or its shared chrome="shell" layout)
//     changed in a way that altered the rendered byte sequence.
//  2. The Astro build pipeline (`mage TemplatesBuildEmbed`) regressed
//     in a way that changed deterministic output.
//
// Re-materialize the golden via UPDATE_GOLDENS=1 after an intentional
// edit, review the diff, then commit. Drift without an intentional
// edit is a CONFIRMED-CE for the L3-E3 stil track.
func TestTemplatesHtmlEmbed_Stil(t *testing.T) {
	t.Parallel()

	embedded := EmbeddedEmbedHTML()

	for _, view := range stilViewNames {
		view := view
		t.Run(view, func(t *testing.T) {
			t.Parallel()

			distPath := filepath.ToSlash(filepath.Join("stil", view, "index.html"))
			got, err := fs.ReadFile(embedded, distPath)
			if err != nil {
				t.Fatalf("read embedded dist file %q: %v", distPath, err)
			}
			assertStilGoldenBytes(t, view+".html", got)
		})
	}
}

// TestTemplatesHtmlEmbed_Stil_TokensPresent regression-pins that every
// stil view dist HTML file references at least one stil-style CSS
// custom property — i.e. a token from the design-system palette is
// reachable from the page.
//
// The regex accepts any of the namespaced families currently emitted
// by web/templates_embed/src/styles/tokens.css:
//   - --stil-* (root palette identity)
//   - --space-* (spacing scale)
//   - --text-* (typography scale)
//   - --bg-* (background tokens)
//
// A file missing the entire family is a CONFIRMED-CE: the shared
// chrome="shell" layout would have to have dropped the tokens.css
// import, or the per-view <style> islands would have to have stopped
// consuming tokens entirely. Either situation breaks the stil design
// system promise for that route.
func TestTemplatesHtmlEmbed_Stil_TokensPresent(t *testing.T) {
	t.Parallel()

	embedded := EmbeddedEmbedHTML()
	tokenRE := regexp.MustCompile(`--(stil|space|text|bg)-`)

	for _, view := range stilViewNames {
		view := view
		t.Run(view, func(t *testing.T) {
			t.Parallel()

			distPath := filepath.ToSlash(filepath.Join("stil", view, "index.html"))
			body, err := fs.ReadFile(embedded, distPath)
			if err != nil {
				t.Fatalf("read embedded dist file %q: %v", distPath, err)
			}
			if !tokenRE.Match(body) {
				t.Fatalf("dist file %q contains no --stil-/--space-/--text-/--bg- token reference", distPath)
			}
		})
	}
}

// TestTemplatesHtmlEmbed_Stil_ZeroJS regression-pins the zero-JS
// guarantee for every stil view dist HTML file. Each file must
// contain ZERO `<script` substring matches — neither inline
// (<script>...) nor sourced (<script src="...">) tags are allowed.
//
// D1's evidence-of-record (see drop_004 L3-E3 D1 build report) is
// that chrome="shell" emits ZERO scripts for stil/* routes;
// inherited stil-layout scripts only attach to thariq's Playground
// (different layout). A regression that pulls a script back into a
// stil route — Astro client directive, layout swap, dependency
// upgrade emitting a runtime — surfaces here as a CONFIRMED-CE.
//
// The substring match is intentionally aggressive: it catches both
// the canonical `<script>` form and attribute-bearing variants like
// `<script src=...>` or `<script type="module">`. The cost (false
// positive if a stil view ever shows literal `<script` in HTML
// content) is acceptable because the project's stil templates do not
// embed code-example snippets containing literal `<script`.
func TestTemplatesHtmlEmbed_Stil_ZeroJS(t *testing.T) {
	t.Parallel()

	embedded := EmbeddedEmbedHTML()
	needle := []byte("<script")

	for _, view := range stilViewNames {
		view := view
		t.Run(view, func(t *testing.T) {
			t.Parallel()

			distPath := filepath.ToSlash(filepath.Join("stil", view, "index.html"))
			body, err := fs.ReadFile(embedded, distPath)
			if err != nil {
				t.Fatalf("read embedded dist file %q: %v", distPath, err)
			}
			if bytes.Contains(body, needle) {
				t.Fatalf("dist file %q contains <script tag — stil routes must be zero-JS", distPath)
			}
		})
	}
}

// assertStilGoldenBytes is the local first-run-materializing golden
// helper for the L3-E3 stil view tests. It mirrors the helper in
// internal/templates_html_basic/render_test.go (assertGoldenBytes) but
// roots fixtures under testdata/stil/ so the embed package's testdata
// surface stays scoped to stil concerns.
//
// Semantics:
//
//   - UPDATE_GOLDENS=1: unconditionally overwrite the on-disk golden
//     with the current bytes. Use after an intentional stil view edit
//     when the diff has been reviewed.
//   - Golden missing: materialize it from the current bytes, then fail
//     the test once so the reviewer is forced to inspect the new
//     fixture before locking it in. The materialized fixture is
//     committed in the same change as the test.
//   - Golden present + bytes match: pass silently.
//   - Golden present + bytes differ: fail loudly with full got/want
//     dumps so the drift is inspectable in the test output.
//
// The name argument is the file stem; convention is "<view>.html" so
// the on-disk artifact lands at testdata/stil/<view>.html.golden,
// keeping the relationship to dist/stil/<view>/index.html obvious to a
// reviewer scanning the testdata tree.
func assertStilGoldenBytes(t *testing.T, name string, got []byte) {
	t.Helper()

	goldenPath := filepath.Join("testdata", "stil", name+".golden")

	if os.Getenv("UPDATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata/stil: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", goldenPath, err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(goldenPath), 0o755); mkErr != nil {
				t.Fatalf("mkdir testdata/stil: %v", mkErr)
			}
			if wErr := os.WriteFile(goldenPath, got, 0o644); wErr != nil {
				t.Fatalf("materialize golden %s: %v", goldenPath, wErr)
			}
			t.Fatalf("materialized golden at %s from current output; review the bytes, then re-run to lock the regression", goldenPath)
		}
		t.Fatalf("read golden %s (re-run with UPDATE_GOLDENS=1 to regenerate): %v", goldenPath, err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("dist drift from golden %s.\n-- got (%d bytes) --\n%s\n-- want (%d bytes) --\n%s",
			goldenPath, len(got), got, len(want), want)
	}
}
