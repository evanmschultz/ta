package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const taskConfig = `
[plans]
file = "plans.toml"
format = "toml"
description = "A unit of work"

[plans.task]
description = "Work item."

[plans.task.fields.id]
type = "string"
required = true
`

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	taDir := filepath.Join(dir, SchemaDirName)
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(taDir, SchemaFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestResolveFindsProjectSchema verifies the happy path: a
// .ta/schema.toml under the project dir resolves to a one-entry
// Sources slice and a populated Registry.
func TestResolveFindsProjectSchema(t *testing.T) {
	projectRoot := t.TempDir()
	wantPath := writeConfig(t, projectRoot, taskConfig)

	res, err := Resolve(projectRoot)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Sources) != 1 || res.Sources[0] != wantPath {
		t.Errorf("Sources = %v, want [%q]", res.Sources, wantPath)
	}
	if _, ok := res.Registry.DBs["plans"]; !ok {
		t.Errorf("plans db missing from registry")
	}
}

// TestResolveNoSchema covers the "project has no .ta/schema.toml yet"
// case. Must return ErrNoSchema so callers can distinguish "uninitialized
// project" from "malformed schema."
func TestResolveNoSchema(t *testing.T) {
	orphanRoot := t.TempDir()

	_, err := Resolve(orphanRoot)
	if !errors.Is(err, ErrNoSchema) {
		t.Fatalf("err = %v, want ErrNoSchema", err)
	}
}

// TestResolveMalformedSchemaPropagates verifies that a broken
// .ta/schema.toml surfaces its parse error rather than ErrNoSchema.
func TestResolveMalformedSchemaPropagates(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "[plans")

	_, err := Resolve(root)
	if err == nil {
		t.Fatal("expected error from malformed schema")
	}
	if errors.Is(err, ErrNoSchema) {
		t.Fatalf("malformed schema must not surface as ErrNoSchema: %v", err)
	}
}

// TestResolveIgnoresHomeLayer proves the §12.11 cascade drop: a
// ~/.ta/schema.toml that used to fold into every project's resolution
// now has zero effect. Even when HOME is set to a dir carrying a schema,
// a project without its own schema returns ErrNoSchema.
func TestResolveIgnoresHomeLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, taskConfig)

	orphanRoot := t.TempDir()
	_, err := Resolve(orphanRoot)
	if !errors.Is(err, ErrNoSchema) {
		t.Fatalf("err = %v, want ErrNoSchema (home-layer must not leak post-§12.11)", err)
	}
}
