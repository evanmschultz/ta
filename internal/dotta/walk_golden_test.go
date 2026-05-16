package dotta_test

// Golden snapshot integration test for dotta.Walk. Mirrors the
// assertSchemaFlowGolden pattern from internal/render/schema_flow_test.go:
// on first run the .golden bytes are materialised, on subsequent runs
// the test enforces byte identity. The TA_GOLDEN_UPDATE env flag opts
// into a deliberate regeneration pass.
//
// The fixture under testdata/walk/realistic/ mimics the smallest
// non-trivial `.ta/` shape: two top-level files (schema.toml,
// index.toml), an `agents/` subtree with two markdown files plus a
// mapping.toml, and a `ta/` subtree with one nested file. Walk should
// surface the two root files, two subtrees (one with a populated
// mapping, one with the zero-value), and zero Skipped entries.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/dotta"
)

// rootPlaceholder is the literal substituted for the temp-dir prefix in
// every absolute path the marshaled Tree carries. Stable across machines.
const rootPlaceholder = "<ROOT>"

// TestWalkGolden_Realistic enumerates the realistic fixture into a
// fresh tempdir copy, marshals the Tree to canonical TOML with the
// tempdir prefix substituted for <ROOT>, and compares against
// testdata/walk/realistic.golden. On first run the golden file is
// materialised; the test logs the write path and passes so the dev can
// review the bytes before locking the regression.
func TestWalkGolden_Realistic(t *testing.T) {
	tmp := copyRealisticFixture(t)

	tree, err := dotta.Walk(tmp)
	if err != nil {
		t.Fatalf("dotta.Walk(%s): %v", tmp, err)
	}

	got := walkTreeToCanonical(t, tree, rootPlaceholder)

	goldenPath := filepath.Join("testdata", "walk", "realistic.golden")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(goldenPath), 0o755); mkErr != nil {
				t.Fatalf("mkdir golden dir: %v", mkErr)
			}
			if wErr := os.WriteFile(goldenPath, got, 0o644); wErr != nil {
				t.Fatalf("materialize golden: %v", wErr)
			}
			t.Logf("wrote golden %s", goldenPath)
			return
		}
		t.Fatalf("read golden %s: %v", goldenPath, err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("Walk output drift from %s.\n-- got --\n%s\n-- want --\n%s\n(rerun with TA_GOLDEN_UPDATE=1 to regenerate after manual review)",
			goldenPath, got, want)
	}
}

// TestWalkGolden_UpdateFlag is the env-gated regeneration pass. With
// TA_GOLDEN_UPDATE unset it skips immediately; with TA_GOLDEN_UPDATE=1
// it re-runs the walk against a fresh tempdir copy and overwrites the
// committed golden. The pass succeeds unconditionally on regeneration —
// the dev reviews the resulting diff under version control before
// committing.
func TestWalkGolden_UpdateFlag(t *testing.T) {
	if os.Getenv("TA_GOLDEN_UPDATE") != "1" {
		t.Skip("set TA_GOLDEN_UPDATE=1 to regenerate")
	}

	tmp := copyRealisticFixture(t)

	tree, err := dotta.Walk(tmp)
	if err != nil {
		t.Fatalf("dotta.Walk(%s): %v", tmp, err)
	}

	got := walkTreeToCanonical(t, tree, rootPlaceholder)

	goldenPath := filepath.Join("testdata", "walk", "realistic.golden")
	if mkErr := os.MkdirAll(filepath.Dir(goldenPath), 0o755); mkErr != nil {
		t.Fatalf("mkdir golden dir: %v", mkErr)
	}
	if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
		t.Fatalf("write golden %s: %v", goldenPath, err)
	}
	t.Logf("regenerated golden %s (%d bytes)", goldenPath, len(got))
}

// walkTreeToCanonical marshals a Tree to TOML and substitutes the
// tree's reported absolute root with rootPlaceholder so every
// downstream AbsPath becomes <ROOT>/relpath. pelletier/go-toml/v2
// sorts map keys deterministically and preserves slice order, so the
// only host-dependent surface that could destabilise the golden is the
// absolute path prefix — which this substitution erases.
func walkTreeToCanonical(t *testing.T, tree dotta.Tree, rootPlaceholder string) []byte {
	t.Helper()

	rootPrefix := tree.Root
	if rootPrefix == "" {
		t.Fatalf("walkTreeToCanonical: tree.Root is empty; cannot anchor <ROOT> substitution")
	}

	out, err := toml.Marshal(tree)
	if err != nil {
		t.Fatalf("toml.Marshal(tree): %v", err)
	}

	// Substitute the absolute root prefix with <ROOT>. tree.Root and
	// every AbsPath inside the tree share the same prefix because Walk
	// builds them via filepath.Join(absRoot, ...) — a single
	// bytes.ReplaceAll cleans the lot.
	out = bytes.ReplaceAll(out, []byte(rootPrefix), []byte(rootPlaceholder))

	return out
}

// copyRealisticFixture copies testdata/walk/realistic/** into a fresh
// t.TempDir() with explicit 0o644 file mode and 0o755 dir mode so the
// resulting Tree.RootFiles[*].Mode and Tree.Subtrees[*].Files[*].Mode
// are deterministic across machines regardless of process umask.
//
// The on-disk fixture is read-only from the test's perspective — this
// helper never writes back to internal/dotta/testdata/walk/realistic/.
//
// macOS resolves t.TempDir() to a /var/folders/... path; filepath.Abs
// inside Walk would otherwise expose /private/var/folders/... drift.
// We resolve symlinks on the temp root before returning so the prefix
// substitution in walkTreeToCanonical matches the AbsPath Walk reports.
func copyRealisticFixture(t *testing.T) string {
	t.Helper()

	src := filepath.Join("testdata", "walk", "realistic")
	dst := t.TempDir()

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree(%s -> %s): %v", src, dst, err)
	}

	resolved, err := filepath.EvalSymlinks(dst)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dst, err)
	}
	return resolved
}

// copyTree recursively copies src into dst, applying canonical 0o644
// to files and 0o755 to directories. It walks the source via
// filepath.WalkDir and intentionally does NOT preserve source modes —
// the goal is byte-stable enumeration metadata, not faithful
// permission replication.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		// Re-chmod to defeat any process-umask drift on the OpenFile call.
		return os.Chmod(target, 0o644)
	})
}
