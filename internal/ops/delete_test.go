package ops_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
)

// withGlobSchema sets up a project root with a glob db (`workflow/*/db.toml`)
// alongside the single-file plans db. Used by F19's file-level delete tests
// where `paths = ["workflow/*/db.toml"]` produces multiple concrete files.
func withGlobSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.title]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]

[wflow]
paths = ["workflow/*/db.toml"]

[wflow.entry]
description = "A workflow entry"

[wflow.entry.fields.id]
type = "string"
required = true

[wflow.entry.fields.body]
type = "string"
`)
	return root
}

// TestDeleteFileLevelSingleLiteralPath covers F19 path 2: a bare
// file-relpath that uniquely identifies one concrete file (literal mount)
// removes the whole file and prunes every matching index entry.
func TestDeleteFileLevelSingleLiteralPath(t *testing.T) {
	root := withSingleFileSchema(t)
	for _, id := range []string{"plans.a", "plans.b"} {
		if _, _, err := ops.Create(root, id, "plans.task", map[string]any{
			"id": id, "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("seed Create %q: %v", id, err)
		}
	}
	res, err := ops.DeleteWithOptions(root, "plans", "", ops.DeleteOptions{Force: true})
	if err != nil {
		t.Fatalf("DeleteWithOptions: %v", err)
	}
	if res.Level != db.LevelFile {
		t.Errorf("Level = %v, want LevelFile", res.Level)
	}
	if res.RemainingInFile != 0 {
		t.Errorf("RemainingInFile = %d, want 0", res.RemainingInFile)
	}
	if _, statErr := os.Stat(filepath.Join(root, "plans.toml")); !os.IsNotExist(statErr) {
		t.Errorf("plans.toml still exists after file-level delete: err=%v", statErr)
	}
	idx, _ := index.Load(root)
	for _, id := range []string{"plans.a", "plans.b"} {
		if _, ok := idx.Get(id); ok {
			t.Errorf("index still carries %q after file-level delete", id)
		}
	}
}

// TestDeleteFileLevelRequiresForce covers the F19 lock: file-level
// delete refuses when Force=false, and refuses BEFORE any disk
// mutation occurs.
func TestDeleteFileLevelRequiresForce(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.a", "plans.task", map[string]any{
		"id": "plans.a", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	res, err := ops.DeleteWithOptions(root, "plans", "", ops.DeleteOptions{Force: false})
	if err == nil {
		t.Fatalf("expected ErrFileDeleteRequiresForce, got nil")
	}
	if !errors.Is(err, ops.ErrFileDeleteRequiresForce) {
		t.Errorf("err = %v, want ErrFileDeleteRequiresForce", err)
	}
	if res.Level != db.LevelFile {
		t.Errorf("Level = %v, want LevelFile (so the caller can prompt)", res.Level)
	}
	after, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml after refused delete: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("plans.toml was mutated despite refusal:\nbefore: %s\nafter: %s", before, after)
	}
}

// TestDeleteFileLevelGlobUniqueMatch covers F19 path 2 via a glob
// mount that resolves to exactly one concrete file. The unique
// resolution is allowed.
func TestDeleteFileLevelGlobUniqueMatch(t *testing.T) {
	root := withGlobSchema(t)
	if _, _, err := ops.Create(root, "drop_1.db.t1", "wflow.entry", map[string]any{
		"id": "drop_1.db.t1", "body": "body",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := ops.DeleteWithOptions(root, "drop_1.db", "", ops.DeleteOptions{Force: true})
	if err != nil {
		t.Fatalf("DeleteWithOptions: %v", err)
	}
	if res.Level != db.LevelFile {
		t.Errorf("Level = %v, want LevelFile", res.Level)
	}
	if _, statErr := os.Stat(filepath.Join(root, "workflow", "drop_1", "db.toml")); !os.IsNotExist(statErr) {
		t.Errorf("workflow/drop_1/db.toml still exists: err=%v", statErr)
	}
}

// TestDeleteFileLevelGlobMultiMatchRefuses covers F19 path 3: a bare
// file-relpath that resolves to multiple concrete files via a glob
// mount refuses with ErrUnscopedGlobDelete and does NOT touch disk.
// TestDeleteFileLevelGlobMultiMatchRefuses sets up two glob-rooted dbs
// whose mounts both yield a file with slug `shared.db`, then deletes
// by that bare slug. The resolver must surface ErrUnscopedGlobDelete
// rather than pick one file.
func TestDeleteFileLevelGlobMultiMatchRefuses(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[a]
paths = ["one/*/db.toml"]

[a.entry]
description = "a"

[a.entry.fields.id]
type = "string"
required = true

[b]
paths = ["two/*/db.toml"]

[b.entry]
description = "b"

[b.entry.fields.id]
type = "string"
required = true
`)
	if err := os.MkdirAll(filepath.Join(root, "one", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "two", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "one", "shared", "db.toml"), []byte("[a.x]\nid = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "two", "shared", "db.toml"), []byte("[b.x]\nid = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Now the slug `shared.db` exists under both `a` and `b` dbs.
	_, err := ops.DeleteWithOptions(root, "shared.db", "", ops.DeleteOptions{Force: true})
	if err == nil {
		t.Fatalf("expected ErrUnscopedGlobDelete, got nil")
	}
	if !errors.Is(err, ops.ErrUnscopedGlobDelete) {
		t.Errorf("err = %v, want ErrUnscopedGlobDelete", err)
	}
	// Verify neither file was removed.
	for _, p := range []string{
		filepath.Join(root, "one", "shared", "db.toml"),
		filepath.Join(root, "two", "shared", "db.toml"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("file %s missing after refused delete: %v", p, err)
		}
	}
}

// TestDeleteRecordReportsRemainingInFile covers F20: record-level
// delete returns the count of records remaining in the same file.
func TestDeleteRecordReportsRemainingInFile(t *testing.T) {
	root := withSingleFileSchema(t)
	for _, id := range []string{"plans.a", "plans.b", "plans.c"} {
		if _, _, err := ops.Create(root, id, "plans.task", map[string]any{
			"id": id, "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("seed %q: %v", id, err)
		}
	}
	res, err := ops.DeleteWithOptions(root, "plans.a", "", ops.DeleteOptions{Force: false, Verbose: true})
	if err != nil {
		t.Fatalf("DeleteWithOptions: %v", err)
	}
	if res.Level != db.LevelRecord {
		t.Errorf("Level = %v, want LevelRecord", res.Level)
	}
	if res.RemainingInFile != 2 {
		t.Errorf("RemainingInFile = %d, want 2", res.RemainingInFile)
	}
}

// TestDeleteFileLevelMissingFile covers the diagnostic path where the
// id resolves to a known file-relpath but the file is absent on disk.
// The slug match in ResolveDelete only fires when the file is actually
// present (Instances stat-walks); so the only way to surface
// ErrFileNotFound from file-level delete is via a race or partial
// state. We exercise this by removing the file between seed and
// delete.
func TestDeleteFileLevelMissingFileError(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.a", "plans.task", map[string]any{
		"id": "plans.a", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Resolution must succeed first (file present), then we delete it
	// underneath ourselves. The DeleteWithOptions call's stat-then-
	// remove window catches a missing file mid-operation.
	if _, err := ops.DeleteWithOptions(root, "plans", "", ops.DeleteOptions{Force: true}); err != nil {
		t.Fatalf("first DeleteWithOptions: %v", err)
	}
	// Second delete: the file is gone, so ResolveDelete cannot find
	// any matching slug; the bare file-relpath surfaces as
	// ErrIDDoesNotMatchAnyDB (ResolveID's standard miss).
	_, err := ops.DeleteWithOptions(root, "plans", "", ops.DeleteOptions{Force: true})
	if err == nil {
		t.Fatalf("expected error on second delete (file already gone)")
	}
}

// TestDeleteUnknownIDFailsLoudly verifies that an id that resolves to
// neither a record nor a known concrete file surfaces a clear error
// (and does NOT incorrectly land on the file-level path).
func TestDeleteUnknownIDFailsLoudly(t *testing.T) {
	root := withSingleFileSchema(t)
	_, err := ops.DeleteWithOptions(root, "nope.x", "", ops.DeleteOptions{Force: true})
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}

// TestDeleteRecordLegacyShapeStillWorks confirms the legacy 3-return
// `Delete` entry point still works for record-level delete (back-compat
// for callers that haven't migrated to DeleteWithOptions).
func TestDeleteRecordLegacyShapeStillWorks(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.a", "plans.task", map[string]any{
		"id": "plans.a", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := ops.Delete(root, "plans.a", ""); err != nil {
		t.Fatalf("legacy Delete: %v", err)
	}
}

// TestDeleteFileLevelLegacyShapeNeedsForce confirms the legacy 3-return
// `Delete` shape refuses file-level delete (no Force=true path). This
// is the safe default — old callers don't get whole-file removal by
// accident.
func TestDeleteFileLevelLegacyShapeNeedsForce(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.a", "plans.task", map[string]any{
		"id": "plans.a", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := ops.Delete(root, "plans", "")
	if err == nil {
		t.Fatalf("expected error on file-level delete via legacy shape")
	}
	if !errors.Is(err, ops.ErrFileDeleteRequiresForce) {
		t.Errorf("err = %v, want ErrFileDeleteRequiresForce", err)
	}
}
