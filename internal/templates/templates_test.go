package templates_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/templates"
)

const sampleTemplate = `
[plans]
file = "plans.toml"
format = "toml"
description = "Example planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

const malformedTemplate = `
[plans]
# missing shape selector and format — fails meta-schema.

[plans.task]
description = "no fields"
`

func TestListMissingRootReturnsNil(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	names, err := templates.List(root)
	if err != nil {
		t.Fatalf("List missing root: %v", err)
	}
	if names != nil {
		t.Errorf("want nil, got %v", names)
	}
}

func TestListEmptyDir(t *testing.T) {
	root := t.TempDir()
	names, err := templates.List(root)
	if err != nil {
		t.Fatalf("List empty: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("want empty, got %v", names)
	}
}

func TestListSortsAndFiltersNonToml(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"zebra.toml", "apple.toml", "ignored.txt", ".hidden.toml"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte(""), 0o644); err != nil {
			t.Fatalf("seed %s: %v", f, err)
		}
	}
	// Sub-directory must be skipped.
	if err := os.Mkdir(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := templates.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"apple", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("idx %d: got %q, want %q", i, got[i], n)
		}
	}
}

func TestLoadHappyPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte(sampleTemplate), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := templates.Load(root, "schema")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != sampleTemplate {
		t.Errorf("Load returned different bytes")
	}
}

func TestLoadMalformedSurfacesParseError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bad.toml")
	if err := os.WriteFile(path, []byte(malformedTemplate), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := templates.Load(root, "bad")
	if err == nil {
		t.Fatal("expected validation error on malformed template")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error missing file path %q: %v", path, err)
	}
}

func TestLoadMissingErrors(t *testing.T) {
	root := t.TempDir()
	_, err := templates.Load(root, "ghost")
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}

func TestSaveValidatesBeforeWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library") // missing; Save creates it.
	// Pre-seed a valid template with the same name so we can prove the
	// malformed Save does not clobber it.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("pre-seed dir: %v", err)
	}
	existing := []byte(sampleTemplate)
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), existing, 0o644); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}

	err := templates.Save(root, "schema", []byte(malformedTemplate))
	if err == nil {
		t.Fatal("expected validation error on malformed Save")
	}
	got, err := os.ReadFile(filepath.Join(root, "schema.toml"))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(got) != string(existing) {
		t.Errorf("Save clobbered existing file despite validation failure: %q", got)
	}
}

func TestSaveCreatesRootAndWrites(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	if err := templates.Save(root, "schema", []byte(sampleTemplate)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "schema.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != sampleTemplate {
		t.Errorf("bytes differ on disk after Save")
	}
}

func TestSaveEmptyNameErrors(t *testing.T) {
	if err := templates.Save(t.TempDir(), "", []byte(sampleTemplate)); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestDeleteHappyPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "schema.toml")
	if err := os.WriteFile(path, []byte(sampleTemplate), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := templates.Delete(root, "schema"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still present after Delete: %v", err)
	}
}

func TestDeleteMissingErrors(t *testing.T) {
	if err := templates.Delete(t.TempDir(), "ghost"); err == nil {
		t.Fatal("expected error removing missing template")
	}
}

func TestRootDefaultsToHomeDotTa(t *testing.T) {
	// Just assert Root resolves without error and ends in ".ta".
	root, err := templates.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if filepath.Base(root) != ".ta" {
		t.Errorf("Root basename = %q, want %q", filepath.Base(root), ".ta")
	}
}

func TestSetRootForTest(t *testing.T) {
	want := t.TempDir()
	restore := templates.SetRootForTest(want)
	defer restore()
	got, err := templates.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
}
