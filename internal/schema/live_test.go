package schema

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLiveSchemaLoads locks the ta project's own live `.ta/schema.toml`
// against the loader. Without this gate, a malformed live block passes
// `mage check` (which never reads the live file) but blows up at the
// next `ta` invocation — the dev felt that pain repeatedly. This test
// resolves the repo root from the test file's own location (climbing
// to the first ancestor containing `go.mod`) so the assertion is
// stable regardless of the working directory the test runner uses.
//
// Asserted invariants:
//   - the live schema parses cleanly under the current grammar (no
//     load error);
//   - the live Registry declares at minimum the five top-level dbs
//     the ta workspace relies on: cascade, roadmap, template_manifest,
//     html_template, target_system. Any of these silently dropping
//     out of the schema breaks downstream cascade + roadmap +
//     templating workflows.
func TestLiveSchemaLoads(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, ".ta", "schema.toml")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read live schema %s: %v", path, err)
	}

	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("LoadBytes(.ta/schema.toml) must succeed; got: %v", err)
	}

	want := []string{
		"cascade",
		"roadmap",
		"template_manifest",
		"html_template",
		"target_system",
	}
	for _, name := range want {
		if _, ok := reg.DBs[name]; !ok {
			t.Errorf("live schema missing required top-level db %q", name)
		}
	}
}

// repoRoot returns the absolute path of the directory containing the
// repository's go.mod, derived from this test file's own location via
// runtime.Caller. Walking from the test file (rather than os.Getwd)
// keeps the resolution stable across test-runner cwd choices.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot locate test file")
	}
	dir := filepath.Dir(here)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod ancestor found starting from %s", filepath.Dir(here))
		}
		dir = parent
	}
}
