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

const taskConfigStatusRequired = `
[plans]
file = "plans.toml"
format = "toml"
description = "A unit of work, status-required override"

[plans.task]
description = "Work item with required status."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]
`

const noteConfig = `
[notes]
file = "notes.toml"
format = "toml"
description = "A note"

[notes.note]
description = "Freeform note."

[notes.note.fields.title]
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

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestResolveFindsSiblingConfig(t *testing.T) {
	isolateHome(t)
	projectRoot := t.TempDir()
	wantPath := writeConfig(t, projectRoot, taskConfig)
	dataFile := filepath.Join(projectRoot, "work", "tasks.toml")
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}

	res, err := Resolve(dataFile)
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

// TestResolveCascadeMerge verifies the cascade-merge semantics: outer schemas
// are retained when inner configs define disjoint dbs, so the inner config
// is additive rather than a replacement.
func TestResolveCascadeMerge(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	outerPath := writeConfig(t, root, taskConfig)
	inner := filepath.Join(root, "subproject")
	innerPath := writeConfig(t, inner, noteConfig)

	dataFile := filepath.Join(inner, "nested", "notes.toml")
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	res, err := Resolve(dataFile)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, ok := res.Registry.DBs["notes"]; !ok {
		t.Errorf("expected inner notes db present")
	}
	if _, ok := res.Registry.DBs["plans"]; !ok {
		t.Errorf("expected outer plans db preserved under cascade-merge")
	}
	// Sources in merge order: root-to-file means outer first, inner second.
	if got := res.Sources; len(got) != 2 || got[0] != outerPath || got[1] != innerPath {
		t.Errorf("Sources = %v, want [%q, %q]", got, outerPath, innerPath)
	}
}

// TestResolveCloserTypeOverrides verifies that when outer and inner define
// the same db, the inner definition wins wholesale per §4.4 — outer dbs
// unique to that layer remain, but same-named dbs are replaced entirely.
func TestResolveCloserTypeOverrides(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	writeConfig(t, root, taskConfig)
	inner := filepath.Join(root, "subproject")
	writeConfig(t, inner, taskConfigStatusRequired)

	dataFile := filepath.Join(inner, "tasks.toml")
	res, err := Resolve(dataFile)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	db, ok := res.Registry.DBs["plans"]
	if !ok {
		t.Fatalf("plans db missing")
	}
	task, ok := db.Types["task"]
	if !ok {
		t.Fatalf("task type missing")
	}
	if _, ok := task.Fields["status"]; !ok {
		t.Errorf("inner task.status field missing — closer db did not override")
	}
	if len(res.Sources) != 2 {
		t.Errorf("Sources = %v, want two entries", res.Sources)
	}
}

func TestResolveHomeIsBase(t *testing.T) {
	home := isolateHome(t)
	homePath := writeConfig(t, home, taskConfig)

	orphanRoot := t.TempDir()
	dataFile := filepath.Join(orphanRoot, "tasks.toml")

	res, err := Resolve(dataFile)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Sources) != 1 || res.Sources[0] != homePath {
		t.Errorf("Sources = %v, want [%q]", res.Sources, homePath)
	}
}

// TestResolveHomeMergesWithAncestor verifies home config is additive to an
// ancestor chain: both sets of dbs survive, and Sources reflects home first,
// then ancestors root-to-file.
func TestResolveHomeMergesWithAncestor(t *testing.T) {
	home := isolateHome(t)
	homePath := writeConfig(t, home, taskConfig)

	projectRoot := t.TempDir()
	projectPath := writeConfig(t, projectRoot, noteConfig)
	dataFile := filepath.Join(projectRoot, "work", "mix.toml")
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	res, err := Resolve(dataFile)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, ok := res.Registry.DBs["plans"]; !ok {
		t.Errorf("home plans db missing")
	}
	if _, ok := res.Registry.DBs["notes"]; !ok {
		t.Errorf("project notes db missing")
	}
	if got := res.Sources; len(got) != 2 || got[0] != homePath || got[1] != projectPath {
		t.Errorf("Sources = %v, want [%q, %q]", got, homePath, projectPath)
	}
}

func TestResolveNoSchema(t *testing.T) {
	isolateHome(t)
	orphanRoot := t.TempDir()
	dataFile := filepath.Join(orphanRoot, "tasks.toml")

	_, err := Resolve(dataFile)
	if !errors.Is(err, ErrNoSchema) {
		t.Fatalf("err = %v, want ErrNoSchema", err)
	}
}

func TestResolveMalformedSchemaPropagates(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	writeConfig(t, root, "[plans")

	dataFile := filepath.Join(root, "tasks.toml")
	_, err := Resolve(dataFile)
	if err == nil {
		t.Fatal("expected error from malformed schema")
	}
	if errors.Is(err, ErrNoSchema) {
		t.Fatalf("malformed schema must not surface as ErrNoSchema: %v", err)
	}
}

func TestResolveHandlesMissingDataFilePath(t *testing.T) {
	isolateHome(t)
	projectRoot := t.TempDir()
	writeConfig(t, projectRoot, taskConfig)

	notYetCreated := filepath.Join(projectRoot, "work", "new.toml")
	res, err := Resolve(notYetCreated)
	if err != nil {
		t.Fatalf("Resolve on non-existent data file: %v", err)
	}
	if _, ok := res.Registry.DBs["plans"]; !ok {
		t.Errorf("expected plans db")
	}
}
