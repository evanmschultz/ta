package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/schema"
)

// resolveDeleteRegistry exercises both literal-single-file and glob
// mounts so the three F19 levels (record / file / glob_root) can each
// be reached.
func resolveDeleteRegistry() schema.Registry {
	return schema.Registry{DBs: map[string]schema.DB{
		"plans": {
			Name:   "plans",
			Paths:  []string{"plans.toml"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"task": {Name: "task"},
			},
		},
		"plan_db": {
			Name:   "plan_db",
			Paths:  []string{"workflow/*/db.toml"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"build_task": {Name: "build_task"},
			},
		},
	}}
}

// TestResolveDeleteRecordLevel confirms a full id (file-relpath +
// bracket-key) returns LevelRecord with the same Resolved view
// ResolveID would produce.
func TestResolveDeleteRecordLevel(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plans.toml"), "[plans.t1]\n")
	r := NewResolver(root, resolveDeleteRegistry())

	res, dbDecl, level, err := r.ResolveDelete("plans.t1")
	if err != nil {
		t.Fatalf("ResolveDelete: %v", err)
	}
	if level != LevelRecord {
		t.Errorf("level = %v, want LevelRecord", level)
	}
	if dbDecl.Name != "plans" {
		t.Errorf("dbDecl.Name = %q, want plans", dbDecl.Name)
	}
	if res.BracketKey != "t1" || res.FileRelPath != "plans" {
		t.Errorf("res = %+v", res)
	}
}

// TestResolveDeleteFileLevelLiteral confirms a bare file-relpath
// against a literal-path mount returns LevelFile.
func TestResolveDeleteFileLevelLiteral(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plans.toml"), "[plans.t1]\n")
	r := NewResolver(root, resolveDeleteRegistry())

	res, dbDecl, level, err := r.ResolveDelete("plans")
	if err != nil {
		t.Fatalf("ResolveDelete: %v", err)
	}
	if level != LevelFile {
		t.Errorf("level = %v, want LevelFile", level)
	}
	if dbDecl.Name != "plans" {
		t.Errorf("dbDecl.Name = %q, want plans", dbDecl.Name)
	}
	if res.BracketKey != "" {
		t.Errorf("BracketKey = %q, want empty for file-level", res.BracketKey)
	}
	if res.FilePath != filepath.Join(root, "plans.toml") {
		t.Errorf("FilePath = %q, want %s", res.FilePath, filepath.Join(root, "plans.toml"))
	}
}

// TestResolveDeleteFileLevelGlobUnique confirms a bare file-relpath
// that matches one concrete file from a glob mount returns LevelFile.
func TestResolveDeleteFileLevelGlobUnique(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "workflow", "drop_1", "db.toml"), "[a]\nx = 1\n")
	r := NewResolver(root, resolveDeleteRegistry())

	res, _, level, err := r.ResolveDelete("drop_1.db")
	if err != nil {
		t.Fatalf("ResolveDelete: %v", err)
	}
	if level != LevelFile {
		t.Errorf("level = %v, want LevelFile", level)
	}
	want := filepath.Join(root, "workflow", "drop_1", "db.toml")
	if res.FilePath != want {
		t.Errorf("FilePath = %q, want %q", res.FilePath, want)
	}
}

// TestResolveDeleteGlobRootRefuses confirms that when two dbs both
// produce the same file-relpath via glob expansion, ResolveDelete
// surfaces ErrUnscopedGlobDelete with both file paths.
func TestResolveDeleteGlobRootRefuses(t *testing.T) {
	root := t.TempDir()
	reg := schema.Registry{DBs: map[string]schema.DB{
		"a": {
			Name:   "a",
			Paths:  []string{"one/*/db.toml"},
			Format: schema.FormatTOML,
			Types:  map[string]schema.SectionType{"e": {Name: "e"}},
		},
		"b": {
			Name:   "b",
			Paths:  []string{"two/*/db.toml"},
			Format: schema.FormatTOML,
			Types:  map[string]schema.SectionType{"e": {Name: "e"}},
		},
	}}
	writeFile(t, filepath.Join(root, "one", "shared", "db.toml"), "[a.x]\n")
	writeFile(t, filepath.Join(root, "two", "shared", "db.toml"), "[b.x]\n")
	r := NewResolver(root, reg)

	_, _, level, err := r.ResolveDelete("shared.db")
	if err == nil {
		t.Fatalf("expected ErrUnscopedGlobDelete, got nil")
	}
	if !errors.Is(err, ErrUnscopedGlobDelete) {
		t.Errorf("err = %v, want ErrUnscopedGlobDelete", err)
	}
	if level != LevelGlobRoot {
		t.Errorf("level = %v, want LevelGlobRoot", level)
	}
	for _, p := range []string{
		filepath.Join(root, "one", "shared", "db.toml"),
		filepath.Join(root, "two", "shared", "db.toml"),
	} {
		if _, statErr := os.Stat(p); statErr != nil {
			t.Errorf("file %s missing after refused resolve: %v", p, statErr)
		}
	}
}

// TestResolveDeleteUnknownIDErrors confirms an id that matches neither
// a record nor a concrete file surfaces a wrapped error (and does not
// silently classify as LevelFile).
func TestResolveDeleteUnknownIDErrors(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root, resolveDeleteRegistry())

	_, _, _, err := r.ResolveDelete("nope")
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}
