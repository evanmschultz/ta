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
	if res.Path != wantPath {
		t.Errorf("Path = %q, want %q", res.Path, wantPath)
	}
	if _, ok := res.Registry.Types["task"]; !ok {
		t.Errorf("task type missing from registry")
	}
}

func TestResolveClosestWins(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	writeConfig(t, root, taskConfig)
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
	if res.Path != innerPath {
		t.Errorf("Path = %q, want %q", res.Path, innerPath)
	}
	if _, ok := res.Registry.Types["note"]; !ok {
		t.Errorf("expected inner note config to win")
	}
	if _, ok := res.Registry.Types["task"]; ok {
		t.Errorf("outer task config should not leak when inner wins")
	}
}

func TestResolveHomeFallback(t *testing.T) {
	home := isolateHome(t)
	homePath := writeConfig(t, home, taskConfig)

	orphanRoot := t.TempDir()
	dataFile := filepath.Join(orphanRoot, "tasks.toml")

	res, err := Resolve(dataFile)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Path != homePath {
		t.Errorf("Path = %q, want home config %q", res.Path, homePath)
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
