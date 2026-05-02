package db

import (
	"errors"
	"os"
	"path/filepath"
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

func TestInstancesSingleFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "README.md"), "# x\n")

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("readme")
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].Slug != "README" {
		t.Errorf("slug = %q, want README", got[0].Slug)
	}
	want := filepath.Join(root, "README.md")
	if got[0].FilePath != want {
		t.Errorf("FilePath = %q, want %q", got[0].FilePath, want)
	}
}

func TestInstancesGlob(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "workflow", "drop_1", "db.toml"), "[a]\nx = 1\n")
	writeFile(t, filepath.Join(root, "workflow", "drop_2", "db.toml"), "[b]\ny = 2\n")
	writeFile(t, filepath.Join(root, "workflow", "loose.toml"), "")
	if err := os.MkdirAll(filepath.Join(root, "workflow", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("plan_db")
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d instances, want 2 (%v)", len(got), slugsOf(got))
	}
	want := map[string]string{
		"drop_1.db": filepath.Join(root, "workflow", "drop_1", "db.toml"),
		"drop_2.db": filepath.Join(root, "workflow", "drop_2", "db.toml"),
	}
	for _, inst := range got {
		w, ok := want[inst.Slug]
		if !ok {
			t.Errorf("unexpected slug %q", inst.Slug)
			continue
		}
		if inst.FilePath != w {
			t.Errorf("slug %q: FilePath = %q, want %q", inst.Slug, inst.FilePath, w)
		}
	}
}

func TestInstancesGlobMissingRoot(t *testing.T) {
	root := t.TempDir() // no workflow/

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("plan_db")
	if err != nil {
		t.Fatalf("Instances with missing root should yield empty, got err %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 instances, got %v", slugsOf(got))
	}
}

func TestInstancesUnknownDB(t *testing.T) {
	r := NewResolver("/proj", testRegistry())
	_, err := r.Instances("nope")
	if err == nil {
		t.Fatal("expected ErrIDDoesNotMatchAnyDB")
	}
	if !errors.Is(err, ErrIDDoesNotMatchAnyDB) {
		t.Errorf("expected ErrIDDoesNotMatchAnyDB, got %v", err)
	}
}

func TestResolveReadSingleFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "README.md"), "# x\n")
	r := NewResolver(root, testRegistry())

	db, inst, abs, err := r.ResolveRead("README.installation")
	if err != nil {
		t.Fatalf("ResolveRead: %v", err)
	}
	if db.Name != "readme" {
		t.Errorf("db.Name = %q", db.Name)
	}
	if inst.Slug != "README" {
		t.Errorf("slug = %q", inst.Slug)
	}
	if abs != filepath.Join(root, "README.md") {
		t.Errorf("abs = %q", abs)
	}
}

func TestResolveReadGlob(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "workflow", "ta", "db.toml"), "[a]\n")

	r := NewResolver(root, testRegistry())
	_, inst, abs, err := r.ResolveRead("ta.db.task_001")
	if err != nil {
		t.Fatalf("ResolveRead: %v", err)
	}
	if inst.Slug != "ta.db" {
		t.Errorf("slug = %q", inst.Slug)
	}
	if abs != filepath.Join(root, "workflow", "ta", "db.toml") {
		t.Errorf("abs = %q", abs)
	}
}

func TestResolveReadFileMissing(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	_, _, _, err := r.ResolveRead("ta.db.task_001")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("expected ErrInstanceNotFound, got %v", err)
	}
}

func TestResolveWriteRejectsHint(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	_, _, _, err := r.ResolveWrite("README.installation", "README.md")
	if err == nil {
		t.Fatal("expected error: hint rejected under F10 grammar")
	}
	if !errors.Is(err, ErrPathHintMismatch) {
		t.Errorf("expected ErrPathHintMismatch, got %v", err)
	}
}

func TestResolveWriteDerivesPath(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	// Glob: target directory does not exist yet.
	_, inst, abs, err := r.ResolveWrite("drop_9.db.task_001", "")
	if err != nil {
		t.Fatalf("ResolveWrite: %v", err)
	}
	if inst.Slug != "drop_9.db" {
		t.Errorf("slug = %q", inst.Slug)
	}
	want := filepath.Join(root, "workflow", "drop_9", "db.toml")
	if abs != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}
}

func TestMatchScope(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	cases := []struct {
		scope, slug string
		want        bool
	}{
		{"reference-*", "reference-api", true},
		{"reference-*", "reference-types", true},
		{"reference-*", "installation", false},
		{"reference-*", "reference", false},
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

func slugsOf(is []Instance) []string {
	out := make([]string, len(is))
	for i, s := range is {
		out[i] = s.Slug
	}
	return out
}
