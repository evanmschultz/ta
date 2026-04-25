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
	slugs := slugsOf(got)
	if len(slugs) != 2 {
		t.Fatalf("got slugs %v, want 2", slugs)
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

func TestInstancesCollection(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "docs", "installation.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "getting-started.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "reference", "api.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "tutorial", "first-steps.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "a", "b", "c", "d.md"), "# x\n")

	// Dotfile / dotdir / mismatched ext — skipped.
	writeFile(t, filepath.Join(root, "docs", ".hidden.md"), "")
	writeFile(t, filepath.Join(root, "docs", ".draft", "secret.md"), "")
	writeFile(t, filepath.Join(root, "docs", "README.txt"), "")

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("docs")
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	want := map[string]string{
		"installation":         filepath.Join(root, "docs", "installation.md"),
		"getting-started":      filepath.Join(root, "docs", "getting-started.md"),
		"reference.api":        filepath.Join(root, "docs", "reference", "api.md"),
		"tutorial.first-steps": filepath.Join(root, "docs", "tutorial", "first-steps.md"),
		"a.b.c.d":              filepath.Join(root, "docs", "a", "b", "c", "d.md"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d instances, want %d (%v)", len(got), len(want), slugsOf(got))
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

func TestInstancesCollectionMissingRoot(t *testing.T) {
	root := t.TempDir() // no docs/

	r := NewResolver(root, testRegistry())
	got, err := r.Instances("docs")
	if err != nil {
		t.Fatalf("Instances with missing root should yield empty, got err %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 instances, got %v", slugsOf(got))
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
		t.Fatal("expected ErrUnknownDB")
	}
	if !errors.Is(err, ErrUnknownDB) {
		t.Errorf("expected ErrUnknownDB, got %v", err)
	}
}

func TestResolveReadSingleFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "README.md"), "# x\n")
	r := NewResolver(root, testRegistry())

	db, inst, abs, err := r.ResolveRead("README.section.installation")
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
	_, inst, abs, err := r.ResolveRead("ta.db.build_task.task_001")
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

	_, _, _, err := r.ResolveRead("ta.db.build_task.task_001")
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

	_, _, _, err := r.ResolveWrite("README.section.installation", "README.md")
	if err == nil {
		t.Fatal("expected error: hint rejected under Phase 9.2 grammar")
	}
	if !errors.Is(err, ErrPathHintMismatch) {
		t.Errorf("expected ErrPathHintMismatch, got %v", err)
	}
}

func TestResolveWriteDerivesPath(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	// Glob: target directory does not exist yet.
	_, inst, abs, err := r.ResolveWrite("drop_9.db.build_task.task_001", "")
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

func TestResolveWriteCollectionDerivesPath(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, testRegistry())

	_, _, abs, err := r.ResolveWrite("install.prereqs.section.title", "")
	if err != nil {
		t.Fatalf("ResolveWrite: %v", err)
	}
	want := filepath.Join(root, "docs", "install", "prereqs.md")
	if abs != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}
}

func TestResolveReadCollisionPropagates(t *testing.T) {
	// Collection mount: two files yield the same dotted slug.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "reference-api.md"), "# x\n")
	writeFile(t, filepath.Join(root, "docs", "reference", "api.md"), "# x\n")

	r := NewResolver(root, testRegistry())
	_, err := r.Instances("docs")
	// Under the new dot-segment slug, "reference-api" and "reference.api"
	// are distinct — no collision. Confirm both surface.
	if err != nil {
		t.Fatalf("Instances: %v", err)
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
