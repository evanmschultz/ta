package index_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

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
paths = ["plans.toml"]
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
paths = ["workflow/*/db.toml"]
description = "multi-instance per drop"

[plan_db.build_task]
description = "build task"

[plan_db.build_task.fields.id]
type = "string"
required = true
description = "id"
`

const mdSchema = `[docs]
paths = ["docs/*.md"]
description = "docs collection"

[docs.section]
description = "section"
heading = 2

[docs.section.fields.body]
type = "string"
description = "body"
`

const fileRecordSchema = `[claude_agents]
paths = ["claude_agents/*.md"]
description = "Claude agent prompts"

[claude_agents.agent]
description = "agent"
record_per = "file"
body_field = "prompt"

[claude_agents.agent.fields.name]
type = "string"
required = true

[claude_agents.agent.fields.prompt]
type = "string"
required = true
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

// TestRebuildFileRecordDB exercises the F38b dispatch fix: index.Rebuild
// against a file-as-record db must NOT trip md.NewBackend's
// `heading must be in [1, 6]` validator. The canonical id for each
// record is the file-relpath itself (no bracket-key, no type segment),
// and the entry's Type is the declared file-as-record type.
func TestRebuildFileRecordDB(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), fileRecordSchema)
	writeFile(t, filepath.Join(root, "claude_agents", "writer.md"),
		"---\nname: writer\n---\nyou are a writer.\n")
	writeFile(t, filepath.Join(root, "claude_agents", "editor.md"),
		"---\nname: editor\n---\nyou are an editor.\n")

	res, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.RecordsIndexed != 2 {
		t.Errorf("RecordsIndexed = %d, want 2 (have: %v)",
			res.RecordsIndexed, keysOf(res.Index.Records))
	}
	wantKeys := []string{"writer", "editor"}
	for _, k := range wantKeys {
		entry, ok := res.Index.Records[k]
		if !ok {
			t.Errorf("missing entry %q; have: %v", k, keysOf(res.Index.Records))
			continue
		}
		if entry.Type != "agent" {
			t.Errorf("entry %q: Type = %q, want agent", k, entry.Type)
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

// TestRebuildPreservesPriorCreated pins F14: when a prior on-disk index
// already carries Created timestamps for canonical ids that survive the
// rebuild, those Created stamps must be carried through rather than
// overwritten with the rebuild's `now`. Updated is always restamped.
// PreservedCount reflects the prior-hit count; FreshCount stays zero
// when every walked id appears in prior.
func TestRebuildPreservesPriorCreated(t *testing.T) {
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

	// Seed a prior index whose Created timestamps predate the rebuild.
	priorCreated := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	priorUpdated := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	prior := &index.Index{
		FormatVersion: index.FormatVersion,
		Records: map[string]index.Entry{
			"plans.task.t1": {Type: "task", Created: priorCreated, Updated: priorUpdated},
			"plans.task.t2": {Type: "task", Created: priorCreated, Updated: priorUpdated},
		},
	}
	if err := prior.Save(root); err != nil {
		t.Fatalf("seed prior: %v", err)
	}

	res, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if res.RecordsIndexed != 2 {
		t.Errorf("RecordsIndexed = %d, want 2", res.RecordsIndexed)
	}
	if res.PreservedCount != 2 {
		t.Errorf("PreservedCount = %d, want 2", res.PreservedCount)
	}
	if res.FreshCount != 0 {
		t.Errorf("FreshCount = %d, want 0", res.FreshCount)
	}

	for _, k := range []string{"plans.task.t1", "plans.task.t2"} {
		entry, ok := res.Index.Records[k]
		if !ok {
			t.Fatalf("missing entry %q", k)
		}
		if !entry.Created.Equal(priorCreated) {
			t.Errorf("entry %q: Created = %v, want preserved %v", k, entry.Created, priorCreated)
		}
		if !entry.Updated.After(priorUpdated) {
			t.Errorf("entry %q: Updated = %v, want > priorUpdated %v", k, entry.Updated, priorUpdated)
		}
	}
}

// TestRebuildStampsFreshOnMissingPrior pins F14 fresh-stamp behavior:
// canonical ids walked on disk that have NO matching prior entry are
// stamped with the rebuild's `now` for both Created and Updated.
// FreshCount reflects the unmatched-walk count; PreservedCount stays
// zero when prior is empty.
func TestRebuildStampsFreshOnMissingPrior(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), singleFileSchema)
	writeFile(t, filepath.Join(root, "plans.toml"),
		`[plans.task.t1]
id = "t1"
title = "first"
`)

	// No prior index file on disk — bestEffortPriorIndex returns empty.
	before := time.Now().UTC().Add(-time.Second)

	res, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if res.RecordsIndexed != 1 {
		t.Errorf("RecordsIndexed = %d, want 1", res.RecordsIndexed)
	}
	if res.PreservedCount != 0 {
		t.Errorf("PreservedCount = %d, want 0", res.PreservedCount)
	}
	if res.FreshCount != 1 {
		t.Errorf("FreshCount = %d, want 1", res.FreshCount)
	}

	entry, ok := res.Index.Records["plans.task.t1"]
	if !ok {
		t.Fatalf("plans.task.t1 missing")
	}
	if entry.Created.Before(before) {
		t.Errorf("entry Created = %v, want >= %v (rebuild stamp)", entry.Created, before)
	}
	if !entry.Created.Equal(entry.Updated) {
		t.Errorf("fresh entry: Created = %v, Updated = %v; want equal", entry.Created, entry.Updated)
	}
}

// TestRebuildFallsBackToNowWhenPriorIndexCorrupt pins the F14 error-class
// allowlist for best-effort prior-index loads. Three corruption shapes
// must all yield empty-prior-map behavior (rebuild proceeds with fresh
// Created stamps everywhere): malformed TOML, unknown format_version,
// and structurally broken entry tables.
func TestRebuildFallsBackToNowWhenPriorIndexCorrupt(t *testing.T) {
	cases := []struct {
		name     string
		priorRaw string
	}{
		{
			name:     "malformed_toml",
			priorRaw: "this is not = valid toml [[[\n",
		},
		{
			name: "unknown_format_version",
			priorRaw: `format_version = 99

[plans.task.t1]
type = "task"
created = 2025-01-02T03:04:05Z
updated = 2025-01-02T03:04:05Z
`,
		},
		{
			name: "missing_format_version_scalar",
			priorRaw: `[plans.task.t1]
type = "task"
created = 2025-01-02T03:04:05Z
updated = 2025-01-02T03:04:05Z
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, ".ta", "schema.toml"), singleFileSchema)
			writeFile(t, filepath.Join(root, "plans.toml"),
				`[plans.task.t1]
id = "t1"
title = "first"
`)
			// Write the corrupt prior index directly (Save would reject it).
			writeFile(t, filepath.Join(root, ".ta", "index.toml"), tc.priorRaw)

			before := time.Now().UTC().Add(-time.Second)

			res, err := index.Rebuild(root)
			if err != nil {
				t.Fatalf("Rebuild: expected best-effort recovery, got error: %v", err)
			}
			if res.RecordsIndexed != 1 {
				t.Errorf("RecordsIndexed = %d, want 1", res.RecordsIndexed)
			}
			if res.PreservedCount != 0 {
				t.Errorf("PreservedCount = %d, want 0 (prior corrupt → empty map)", res.PreservedCount)
			}
			if res.FreshCount != 1 {
				t.Errorf("FreshCount = %d, want 1", res.FreshCount)
			}

			entry, ok := res.Index.Records["plans.task.t1"]
			if !ok {
				t.Fatalf("plans.task.t1 missing")
			}
			if entry.Created.Before(before) {
				t.Errorf("entry Created = %v, want >= %v (fresh stamp)", entry.Created, before)
			}
		})
	}
}

// TestRebuildPropagatesPermissionError pins the F14 error-class allowlist
// negative case: permission / I/O errors from os.ReadFile on the prior
// index must NOT be silently swallowed. A transient EACCES that gets
// classified as "empty prior map" would erase historical Created
// timestamps across every record on disk — exactly what F14 forbids.
//
// Skipped when running as root (chmod 0 is bypassed by uid 0) and on
// Windows (POSIX mode bits don't apply).
func TestRebuildPropagatesPermissionError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits do not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses chmod 0; permission propagation cannot be exercised")
	}

	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), singleFileSchema)
	writeFile(t, filepath.Join(root, "plans.toml"),
		`[plans.task.t1]
id = "t1"
title = "first"
`)

	// Seed a valid prior index, then make it unreadable.
	prior := &index.Index{
		FormatVersion: index.FormatVersion,
		Records: map[string]index.Entry{
			"plans.task.t1": {
				Type:    "task",
				Created: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
				Updated: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
			},
		},
	}
	if err := prior.Save(root); err != nil {
		t.Fatalf("seed prior: %v", err)
	}
	indexPath := index.Path(root)
	if err := os.Chmod(indexPath, 0); err != nil {
		t.Fatalf("chmod 0 on prior index: %v", err)
	}
	// Restore mode before t.TempDir cleanup so the test dir can be
	// removed even if the test fails mid-flight.
	t.Cleanup(func() {
		_ = os.Chmod(indexPath, 0o644)
	})

	_, err := index.Rebuild(root)
	if err == nil {
		t.Fatal("Rebuild: expected permission error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "load prior index") {
		t.Errorf("Rebuild err = %v, want wrapped under 'load prior index'", err)
	}
}

// TestRebuildPopulatesDBName pins the F38d-2.14c fast-path contract:
// every entry produced by rebuild MUST carry the authoritative DBName
// in Entry.DBName. Without this, resolveIDWithIndexHint at
// internal/ops/helpers.go falls back to the alphabetical-scan slow
// path and the ambiguous-id failure mode F38d-2.14c was designed to
// prevent re-appears immediately after every `ta index rebuild`.
func TestRebuildPopulatesDBName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ta", "schema.toml"), singleFileSchema)
	writeFile(t, filepath.Join(root, "plans.toml"),
		`[plans.t1]
id = "t1"
title = "first"
`)

	result, err := index.Rebuild(root)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if result.RecordsIndexed == 0 {
		t.Fatalf("Rebuild produced zero entries; want >=1")
	}

	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry, ok := idx.Get("plans.t1")
	if !ok {
		t.Fatalf("index missing entry for plans.t1; entries: %v", idx.Records)
	}
	if entry.DBName != "plans" {
		t.Errorf("entry.DBName = %q, want %q (F38d-2.14c regression: rebuild must populate DBName)", entry.DBName, "plans")
	}
}
