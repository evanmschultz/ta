package index_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/evanmschultz/ta/internal/index"
)

// writeFile is a test helper that ensures the parent dir exists before
// writing — matches the pattern used in internal/db/instance_test.go.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const singleFileSchema = `[plans]
paths = ["plans"]
format = "toml"
description = "single-file db"

[plans.task]
description = "tasks"

[plans.task.fields.id]
type = "string"
required = true
description = "id"

[plans.task.fields.title]
type = "string"
required = true
description = "title"
`

const multiFileSchema = `[plan_db]
paths = ["workflow/*/db"]
format = "toml"
description = "multi-instance per drop"

[plan_db.build_task]
description = "build task"

[plan_db.build_task.fields.id]
type = "string"
required = true
description = "id"
`

const mdSchema = `[docs]
paths = ["docs/"]
format = "md"
description = "docs collection"

[docs.section]
description = "section"
heading = 2

[docs.section.fields.body]
type = "string"
description = "body"
`

func TestRebuildEmptyProjectProducesEmptyIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), singleFileSchema)

	res, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.RecordsIndexed != 0 {
		t.Errorf("RecordsIndexed = %d, want 0", res.RecordsIndexed)
	}
	if res.IndexPath != index.Path(root) {
		t.Errorf("IndexPath = %q, want %q", res.IndexPath, index.Path(root))
	}
	// File must exist on disk even when empty so the orchestrator can
	// observe rebuild progress.
	if _, err := os.Stat(index.Path(root)); err != nil {
		t.Errorf("stat index file: %v", err)
	}
}

func TestRebuildSingleFileTOML(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), singleFileSchema)
	writeFile(t, filepath.Join(root, "plans.toml"),
		`[plans.task.t1]
id = "t1"
title = "first"

[plans.task.t2]
id = "t2"
title = "second"
`)

	res, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.RecordsIndexed != 2 {
		t.Errorf("RecordsIndexed = %d, want 2", res.RecordsIndexed)
	}
	want := map[string]string{
		"plans.task.t1": "task",
		"plans.task.t2": "task",
	}
	for k, wantType := range want {
		got, ok := res.Index.Records[k]
		if !ok {
			t.Errorf("missing entry %q; have: %v", k, keysOf(res.Index.Records))
			continue
		}
		if got.Type != wantType {
			t.Errorf("entry %q: Type = %q, want %q", k, got.Type, wantType)
		}
	}
}

func TestRebuildMultiFileTOML(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), multiFileSchema)
	writeFile(t, filepath.Join(root, "workflow", "drop_a", "db.toml"),
		`[build_task.task_001]
id = "task_001"
`)
	writeFile(t, filepath.Join(root, "workflow", "drop_b", "db.toml"),
		`[build_task.task_002]
id = "task_002"

[build_task.task_003]
id = "task_003"
`)

	res, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.RecordsIndexed != 3 {
		t.Errorf("RecordsIndexed = %d, want 3 (have: %v)", res.RecordsIndexed, keysOf(res.Index.Records))
	}
	want := []string{
		"drop_a.db.build_task.task_001",
		"drop_b.db.build_task.task_002",
		"drop_b.db.build_task.task_003",
	}
	for _, k := range want {
		entry, ok := res.Index.Records[k]
		if !ok {
			t.Errorf("missing entry %q; have: %v", k, keysOf(res.Index.Records))
			continue
		}
		if entry.Type != "build_task" {
			t.Errorf("entry %q: Type = %q, want build_task", k, entry.Type)
		}
	}
}

func TestRebuildMD(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), mdSchema)
	writeFile(t, filepath.Join(root, "docs", "guide.md"),
		`## Installation

Install instructions here.

## Configuration

Config goes here.
`)

	res, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.RecordsIndexed != 2 {
		t.Errorf("RecordsIndexed = %d, want 2 (have: %v)", res.RecordsIndexed, keysOf(res.Index.Records))
	}
	wantKeys := []string{
		"guide.section.installation",
		"guide.section.configuration",
	}
	for _, k := range wantKeys {
		entry, ok := res.Index.Records[k]
		if !ok {
			t.Errorf("missing entry %q; have: %v", k, keysOf(res.Index.Records))
			continue
		}
		if entry.Type != "section" {
			t.Errorf("entry %q: Type = %q, want section", k, entry.Type)
		}
	}
}

func TestRebuildSkipsMissingMounts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), multiFileSchema)
	// No workflow/ directory — rebuild must not error.

	res, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.RecordsIndexed != 0 {
		t.Errorf("RecordsIndexed = %d, want 0", res.RecordsIndexed)
	}
}

func TestRebuildPersistsToDisk(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), singleFileSchema)
	writeFile(t, filepath.Join(root, "plans.toml"),
		`[plans.task.t1]
id = "t1"
title = "first"
`)
	if _, err := index.Rebuild(root); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Loading from disk must show the same entry — proves the rebuild
	// actually persisted, not just returned an in-memory result.
	loaded, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := loaded.Records["plans.task.t1"]; !ok {
		t.Errorf("plans.task.t1 missing from on-disk index: %v", keysOf(loaded.Records))
	}
}

func TestRebuildOverwritesExistingIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), singleFileSchema)
	writeFile(t, filepath.Join(root, "plans.toml"),
		`[plans.task.t1]
id = "t1"
title = "first"
`)
	// Seed an index with a stale entry that does NOT exist on disk.
	stale := &index.Index{
		FormatVersion: index.FormatVersion,
		Records: map[string]index.Entry{
			"plans.task.t99": {Type: "task"},
		},
	}
	if err := stale.Save(root); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := index.Rebuild(root); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	loaded, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := loaded.Records["plans.task.t99"]; ok {
		t.Errorf("stale entry plans.task.t99 survived rebuild: %v", keysOf(loaded.Records))
	}
	if _, ok := loaded.Records["plans.task.t1"]; !ok {
		t.Errorf("real entry plans.task.t1 missing after rebuild: %v", keysOf(loaded.Records))
	}
}

func TestRebuildMissingSchemaErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := index.Rebuild(root); err == nil {
		t.Fatal("Rebuild: expected error when .ta/schema.toml is missing")
	}
}

func TestRebuildEmptyProjectRootErrors(t *testing.T) {
	if _, err := index.Rebuild(""); err == nil {
		t.Fatal("Rebuild: expected error for empty project root")
	}
}

func keysOf(m map[string]index.Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
