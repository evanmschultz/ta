package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const taskConfig = `
[schema.task]
description = "A unit of work"

[schema.task.fields.id]
type = "string"
required = true
`

const taskConfigStatusRequired = `
[schema.task]
description = "A unit of work, status-required override"

[schema.task.fields.id]
type = "string"
required = true

[schema.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]
`

const noteConfig = `
[schema.note]
description = "A note"

[schema.note.fields.title]
type = "string"
required = true
`

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	taDir := filepath.Join(dir, ConfigDirName)
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(taDir, ConfigFileName)
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
	if _, ok := res.Registry.Types["task"]; !ok {
		t.Errorf("task type missing from registry")
	}
}

// TestResolveCascadeMerge verifies the cascade-merge semantics: outer schemas
// are retained when inner configs define disjoint types, so the inner config
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
	if _, ok := res.Registry.Types["note"]; !ok {
		t.Errorf("expected inner note type present")
	}
	if _, ok := res.Registry.Types["task"]; !ok {
		t.Errorf("expected outer task type preserved under cascade-merge")
	}
	// Sources in merge order: root-to-file means outer first, inner second.
	if got := res.Sources; len(got) != 2 || got[0] != outerPath || got[1] != innerPath {
		t.Errorf("Sources = %v, want [%q, %q]", got, outerPath, innerPath)
	}
}

// TestResolveCloserTypeOverrides verifies that when outer and inner define
// the same section type, the inner definition wins per-type while other
// outer types remain.
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
	task, ok := res.Registry.Types["task"]
	if !ok {
		t.Fatalf("task type missing")
	}
	if _, ok := task.Fields["status"]; !ok {
		t.Errorf("inner task.status field missing — closer type did not override")
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
// ancestor chain: both sets of types survive, and Sources reflects home first,
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
	if _, ok := res.Registry.Types["task"]; !ok {
		t.Errorf("home task type missing")
	}
	if _, ok := res.Registry.Types["note"]; !ok {
		t.Errorf("project note type missing")
	}
	if got := res.Sources; len(got) != 2 || got[0] != homePath || got[1] != projectPath {
		t.Errorf("Sources = %v, want [%q, %q]", got, homePath, projectPath)
	}
}

func TestResolveNoConfig(t *testing.T) {
	isolateHome(t)
	orphanRoot := t.TempDir()
	dataFile := filepath.Join(orphanRoot, "tasks.toml")

	_, err := Resolve(dataFile)
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("err = %v, want ErrNoConfig", err)
	}
}

func TestResolveMalformedConfigPropagates(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	writeConfig(t, root, "[schema.task")

	dataFile := filepath.Join(root, "tasks.toml")
	_, err := Resolve(dataFile)
	if err == nil {
		t.Fatal("expected error from malformed config")
	}
	if errors.Is(err, ErrNoConfig) {
		t.Fatalf("malformed config must not surface as ErrNoConfig: %v", err)
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
	if _, ok := res.Registry.Types["task"]; !ok {
		t.Errorf("expected task type")
	}
}
