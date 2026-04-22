package db

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a test helper that ensures the parent dir exists and writes
// content to the named path.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestInstancesSingleInstance(t *testing.T) {
	r := NewResolver("/proj", testRegistry())
	got, err := r.Instances("readme")
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].Slug != "" {
		t.Errorf("single-instance slug should be empty, got %q", got[0].Slug)
	}
	want := filepath.Join("/proj", "README.md")
	if got[0].FilePath != want {
		t.Errorf("FilePath = %q, want %q", got[0].FilePath, want)
	}
}

func TestInstancesDirPerInstance(t *testing.T) {
	root := t.TempDir()

	// Canonical instances — each has a db.toml.
	writeFile(t, filepath.Join(root, "workflow", "drop_1", "db.toml"), "[a]\nx = 1\n")
	writeFile(t, filepath.Join(root, "workflow", "drop_2", "db.toml"), "[b]\ny = 2\n")

	// Stray file at workflow/ root — not an instance.
	writeFile(t, filepath.Join(root, "workflow", "loose.toml"), "")

	// Subdir without canonical file — not an instance.
	if err := os.MkdirAll(filepath.Join(root, "workflow", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Nested canonical file — must NOT surface as a top-level instance.
	writeFile(t, filepath.Join(root, "workflow", "drop_1", "nested", "db.toml"), "")

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("plan_db")
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	slugs := slugsOf(got)
	if len(slugs) != 2 {
		t.Fatalf("got slugs %v, want 2", slugs)
	}
	if !contains(slugs, "drop_1") || !contains(slugs, "drop_2") {
		t.Errorf("expected drop_1 and drop_2, got %v", slugs)
	}
	// Verify DirPath and FilePath.
	for _, inst := range got {
		wantDir := filepath.Join(root, "workflow", inst.Slug)
		wantFile := filepath.Join(wantDir, "db.toml")
		if inst.DirPath != wantDir {
			t.Errorf("DirPath = %q, want %q", inst.DirPath, wantDir)
		}
		if inst.FilePath != wantFile {
			t.Errorf("FilePath = %q, want %q", inst.FilePath, wantFile)
		}
	}
}

func TestInstancesFilePerInstance(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "docs", "installation.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "getting-started.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "reference", "api.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "tutorial", "first-steps.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "a", "b", "c", "d.md"), "# x\n")

	// Dotfile — skipped.
	writeFile(t, filepath.Join(root, "docs", ".hidden.md"), "")
	// Dotdir — skipped (rooted dot prefix).
	writeFile(t, filepath.Join(root, "docs", ".draft", "secret.md"), "")
	// Mismatched extension — skipped.
	writeFile(t, filepath.Join(root, "docs", "README.txt"), "")

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("docs")
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	want := map[string]string{
		"installation":         filepath.Join(root, "docs", "installation.md"),
		"getting-started":      filepath.Join(root, "docs", "getting-started.md"),
		"reference-api":        filepath.Join(root, "docs", "reference", "api.md"),
		"tutorial-first-steps": filepath.Join(root, "docs", "tutorial", "first-steps.md"),
		"a-b-c-d":              filepath.Join(root, "docs", "a", "b", "c", "d.md"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d instances, want %d (%v)", len(got), len(want), slugsOf(got))
	}
	for _, inst := range got {
		wantPath, ok := want[inst.Slug]
		if !ok {
			t.Errorf("unexpected slug %q", inst.Slug)
			continue
		}
		if inst.FilePath != wantPath {
			t.Errorf("slug %q: FilePath = %q, want %q", inst.Slug, inst.FilePath, wantPath)
		}
	}
}

func TestInstancesCollectionMissingRoot(t *testing.T) {
	root := t.TempDir() // no docs/ at all

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("docs")
	if err != nil {
		t.Fatalf("Instances with missing root should yield empty, got err %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 instances, got %v", slugsOf(got))
	}
}

func TestInstancesDirectoryMissingRoot(t *testing.T) {
	root := t.TempDir() // no workflow/ at all

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("plan_db")
	if err != nil {
		t.Fatalf("Instances with missing root should yield empty, got err %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 instances, got %v", slugsOf(got))
	}
}

func TestInstancesSlugCollision(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "reference-api.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "reference", "api.md"), "# x\n")

	r := NewResolver(root, testRegistry())
	_, err := r.Instances("docs")
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !errors.Is(err, ErrSlugCollision) {
		t.Errorf("expected ErrSlugCollision, got %v", err)
	}
	if !strings.Contains(err.Error(), "reference-api.md") || !strings.Contains(err.Error(), filepath.Join("reference", "api.md")) {
		t.Errorf("collision error must mention both paths, got: %v", err)
	}
}

func TestInstancesUnknownDB(t *testing.T) {
	r := NewResolver("/proj", testRegistry())
	_, err := r.Instances("nope")
	if err == nil {
		t.Fatal("expected ErrUnknownDB")
	}
	if !errors.Is(err, ErrUnknownDB) {
		t.Errorf("expected ErrUnknownDB, got %v", err)
	}
}

func TestResolveReadSingleInstance(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	db, inst, abs, err := r.ResolveRead("readme.section.installation")
	if err != nil {
		t.Fatalf("ResolveRead: %v", err)
	}
	if db.Name != "readme" {
		t.Errorf("db.Name = %q", db.Name)
	}
	if inst.Slug != "" {
		t.Errorf("slug should be empty, got %q", inst.Slug)
	}
	if abs != filepath.Join(root, "README.md") {
		t.Errorf("abs = %q", abs)
	}
}

func TestResolveReadDirPerInstance(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "workflow", "drop_1", "db.toml"), "[a]\n")

	r := NewResolver(root, testRegistry())
	_, inst, abs, err := r.ResolveRead("plan_db.drop_1.build_task.task_001")
	if err != nil {
		t.Fatalf("ResolveRead: %v", err)
	}
	if inst.Slug != "drop_1" {
		t.Errorf("slug = %q", inst.Slug)
	}
	if abs != filepath.Join(root, "workflow", "drop_1", "db.toml") {
		t.Errorf("abs = %q", abs)
	}
}

func TestResolveReadInstanceMissing(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	_, _, _, err := r.ResolveRead("plan_db.drop_9.build_task.task_001")
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
	if !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("expected ErrInstanceNotFound, got %v", err)
	}
}

func TestResolveReadCollisionPropagates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "reference-api.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "reference", "api.md"), "# x\n")

	r := NewResolver(root, testRegistry())
	_, _, _, err := r.ResolveRead("docs.reference-api.section.endpoints")
	if err == nil {
		t.Fatal("expected collision error at read time")
	}
	if !errors.Is(err, ErrSlugCollision) {
		t.Errorf("expected ErrSlugCollision, got %v", err)
	}
}

func TestMatchScope(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	// Single-instance prefix doesn't accept wildcards (no instance segment).
	cases := []struct {
		scope, slug string
		want        bool
	}{
		{"reference-*", "reference-api", true},
		{"reference-*", "reference-types", true},
		{"reference-*", "installation", false},
		{"reference-*", "reference", false}, // no suffix after hyphen required
		{"*", "anything", true},
		{"exact", "exact", true},
		{"exact", "exact-ish", false},
	}
	for _, tc := range cases {
		if got := r.MatchSlug(tc.scope, tc.slug); got != tc.want {
			t.Errorf("MatchSlug(%q, %q) = %v, want %v", tc.scope, tc.slug, got, tc.want)
		}
	}
}

func TestResolveWritePathHintNewInstanceFlat(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	_, inst, abs, err := r.ResolveWrite("docs.reference-api.section.endpoints", "")
	if err != nil {
		t.Fatalf("ResolveWrite: %v", err)
	}
	if inst.Slug != "reference-api" {
		t.Errorf("slug = %q", inst.Slug)
	}
	want := filepath.Join(root, "docs", "reference-api.md")
	if abs != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}
}

func TestResolveWritePathHintNewInstanceNested(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	_, _, abs, err := r.ResolveWrite("docs.reference-api.section.endpoints", "reference/api.md")
	if err != nil {
		t.Fatalf("ResolveWrite: %v", err)
	}
	want := filepath.Join(root, "docs", "reference", "api.md")
	if abs != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}
}

func TestResolveWriteHintMismatch(t *testing.T) {
	root := t.TempDir()
	// Existing instance at flat path.
	writeFile(t, filepath.Join(root, "docs", "reference-api.md"), "# x\n")

	r := NewResolver(root, testRegistry())
	// Calling with a different hint should error.
	_, _, _, err := r.ResolveWrite("docs.reference-api.section.endpoints", "reference/api.md")
	if err == nil {
		t.Fatal("expected hint-mismatch error")
	}
	if !errors.Is(err, ErrPathHintMismatch) {
		t.Errorf("expected ErrPathHintMismatch, got %v", err)
	}
}

func TestResolveWriteHintMatchExisting(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "reference", "api.md"), "# x\n")

	r := NewResolver(root, testRegistry())
	_, _, abs, err := r.ResolveWrite("docs.reference-api.section.endpoints", "reference/api.md")
	if err != nil {
		t.Fatalf("ResolveWrite: %v", err)
	}
	if abs != filepath.Join(root, "docs", "reference", "api.md") {
		t.Errorf("abs = %q", abs)
	}
}

func TestResolveWriteEmptyHintExistingInstance(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "reference", "api.md"), "# x\n")

	r := NewResolver(root, testRegistry())
	_, _, abs, err := r.ResolveWrite("docs.reference-api.section.endpoints", "")
	if err != nil {
		t.Fatalf("ResolveWrite: %v", err)
	}
	// Empty hint on existing must resolve to the existing path (not create a flat override).
	if abs != filepath.Join(root, "docs", "reference", "api.md") {
		t.Errorf("abs = %q, want existing nested path", abs)
	}
}

func TestResolveWriteSingleInstanceRejectsHint(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	_, _, _, err := r.ResolveWrite("readme.section.installation", "README.md")
	if err == nil {
		t.Fatal("expected error: hint on single-instance db")
	}
}

// TestResolveWriteDirPerInstanceRejectsHint covers the dir-per-instance
// case explicitly: the canonical filename is fixed, so a path_hint is
// meaningless and must error. The constraint is documented on
// ResolveWrite but only lightly exercised before — see V2-PLAN §5.5.1
// "canonical filename required; no per-db filename configuration".
func TestResolveWriteDirPerInstanceRejectsHint(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	_, _, _, err := r.ResolveWrite("plan_db.drop_9.build_task.task_001", "custom.toml")
	if err == nil {
		t.Fatal("expected error: hint on dir-per-instance db")
	}
	if !errors.Is(err, ErrBadAddress) {
		t.Errorf("expected ErrBadAddress, got %v", err)
	}
}

// TestResolveWriteHintRejectsParentEscape covers V2-PLAN §11.D: a
// path_hint that escapes the collection root via '..' segments, absolute
// paths, or cleaned-away '..' elements must be rejected. filepath.IsLocal
// (Go 1.20+) is the lexical check.
func TestResolveWriteHintRejectsParentEscape(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	// Each hint here maps to a slug of "x-md" or similar but must fail
	// earlier on the IsLocal guard regardless of slug match.
	cases := []struct {
		name, hint string
	}{
		{"parent", "../x.md"},
		{"double-parent", "../../etc/passwd"},
		{"rooted-parent-via-clean", "a/../../b.md"},
		{"dot-slash-parent", "./../b.md"},
		{"absolute", "/etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := r.ResolveWrite("docs.reference-api.section.endpoints", tc.hint)
			if err == nil {
				t.Fatalf("hint %q: expected error, got nil", tc.hint)
			}
			if !errors.Is(err, ErrPathHintMismatch) {
				t.Errorf("hint %q: expected ErrPathHintMismatch, got %v", tc.hint, err)
			}
		})
	}
}

func TestResolveWriteDirPerInstanceAutoCreatePath(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	// No drop_9 yet — should return the canonical target path regardless.
	_, inst, abs, err := r.ResolveWrite("plan_db.drop_9.build_task.task_001", "")
	if err != nil {
		t.Fatalf("ResolveWrite: %v", err)
	}
	if inst.Slug != "drop_9" {
		t.Errorf("slug = %q", inst.Slug)
	}
	want := filepath.Join(root, "workflow", "drop_9", "db.toml")
	if abs != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}
}

func slugsOf(is []Instance) []string {
	out := make([]string, len(is))
	for i, s := range is {
		out[i] = s.Slug
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
