package templates_html_embed

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestEmbeddedEmbed_DistRootAccessible verifies the //go:embed all:dist
// directive captures at least one file at the dist/ root and that the
// rebased fs.FS returned by EmbeddedEmbedHTML walks cleanly.
func TestEmbeddedEmbed_DistRootAccessible(t *testing.T) {
	embedded := EmbeddedEmbedHTML()
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		t.Fatalf("fs.ReadDir on EmbeddedEmbedHTML root: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("EmbeddedEmbedHTML root has zero entries; //go:embed all:dist captured nothing")
	}
}

// TestEmbeddedEmbed_DistKeepPresent verifies the .keep placeholder is
// present in the embed at fresh-clone time. The .keep file seeds the
// embed so //go:embed all:dist has at least one file before
// `mage TemplatesBuildEmbed` has run.
func TestEmbeddedEmbed_DistKeepPresent(t *testing.T) {
	embedded := EmbeddedEmbedHTML()
	f, err := embedded.Open(".keep")
	if err != nil {
		t.Fatalf("open .keep via EmbeddedEmbedHTML: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat .keep via EmbeddedEmbedHTML: %v", err)
	}
	if info.IsDir() {
		t.Fatalf(".keep is a directory; expected a regular file")
	}
}

// TestEmbeddedEmbed_NoDoubleEmbedFromExamplesScope regression-pins the
// disjoint-top-level-paths invariant: the repo-root //go:embed
// all:examples directive (embed.go:19) embeds the examples/ tree, and
// Track B's authoring source lives under web/templates_embed/. The
// two paths must be siblings at the repo root — neither nested under
// the other — so the examples scope cannot accidentally pull in
// Track B sources.
//
// Verifying the property by reading the actual file layout
// (examples/ is a sibling of web/, both at the repo root) is the
// canonical proof; testing through the embedded.FS of the root
// package would require importing the root package which is not
// useful from an internal/ test (and would create an awkward
// dependency direction).
func TestEmbeddedEmbed_NoDoubleEmbedFromExamplesScope(t *testing.T) {
	root := findModuleRoot(t)

	// Both directories must exist as direct children of the module root.
	examplesPath := filepath.Join(root, "examples")
	templatesEmbedPath := filepath.Join(root, "web", "templates_embed")

	if info, err := os.Stat(examplesPath); err != nil {
		t.Fatalf("stat %s: %v", examplesPath, err)
	} else if !info.IsDir() {
		t.Fatalf("%s is not a directory", examplesPath)
	}

	if info, err := os.Stat(templatesEmbedPath); err != nil {
		t.Fatalf("stat %s: %v", templatesEmbedPath, err)
	} else if !info.IsDir() {
		t.Fatalf("%s is not a directory", templatesEmbedPath)
	}

	// templates_embed must NOT live under examples/ — that would be
	// the double-embed footgun this test pins against.
	rel, err := filepath.Rel(examplesPath, templatesEmbedPath)
	if err != nil {
		t.Fatalf("filepath.Rel(%s, %s): %v", examplesPath, templatesEmbedPath, err)
	}
	// If templates_embed were under examples, the rel path would not
	// start with ".." — it would be a forward descent.
	if len(rel) < 2 || rel[:2] != ".." {
		t.Fatalf("web/templates_embed/ is not disjoint from examples/: rel = %q (expected to start with \"..\")", rel)
	}
}

// findModuleRoot walks up from the current test file's directory
// until it finds a go.mod, returning the absolute path of the
// containing directory. Fails the test if go.mod is not found before
// the filesystem root.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed; cannot locate test source")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root without finding go.mod from %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}
