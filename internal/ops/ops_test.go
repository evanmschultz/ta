package ops_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
)

// schemaPath returns the project-local schema path.
func schemaPath(root string) string {
	return filepath.Join(root, ".ta", "schema.toml")
}

// writeSchema writes the project-local schema with the given contents.
func writeSchema(t *testing.T, root, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath(root), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force the cache to reload by resetting it.
	ops.SwapDefaultCacheLoaderForTest(func(p string) (config.Resolution, error) {
		return config.Resolve(p)
	})
}

// withSingleFileSchema sets up a project root with a plans db backed
// by plans.toml.
func withSingleFileSchema(t *testing.T) string {
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
`)
	return root
}

func TestCreateRequiresType(t *testing.T) {
	root := withSingleFileSchema(t)
	_, _, err := ops.Create(root, "plans.demo-1", "", map[string]any{
		"id": "demo-1", "title": "x", "status": "todo",
	})
	if err == nil {
		t.Fatal("expected error for empty type")
	}
	if !errors.Is(err, ops.ErrTypeMismatch) {
		t.Errorf("err = %v, want ErrTypeMismatch", err)
	}
}

func TestCreateRejectsBareType(t *testing.T) {
	root := withSingleFileSchema(t)
	_, _, err := ops.Create(root, "plans.demo-1", "task", map[string]any{
		"id": "demo-1", "title": "x", "status": "todo",
	})
	if err == nil {
		t.Fatal("expected error for bare type")
	}
	if !errors.Is(err, ops.ErrTypeNotQualified) {
		t.Errorf("err = %v, want ErrTypeNotQualified", err)
	}
}

func TestCreateRoundTrip(t *testing.T) {
	root := withSingleFileSchema(t)
	_, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "title": "first", "status": "todo",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify the on-disk bracket is `[plans.demo-1]` (not `[plans.task.demo-1]`).
	data, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	body := string(data)
	if !contains(body, "[plans.demo-1]") {
		t.Errorf("plans.toml missing `[plans.demo-1]` bracket; body:\n%s", body)
	}
	if contains(body, "[plans.task.demo-1]") {
		t.Errorf("plans.toml carries legacy `[plans.task.demo-1]` bracket; body:\n%s", body)
	}

	// Verify the index entry.
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	if idx.FormatVersion != 2 {
		t.Errorf("index format_version = %d, want 2", idx.FormatVersion)
	}
	entry, ok := idx.Get("plans.demo-1")
	if !ok {
		t.Fatal("index missing entry for `plans.demo-1`")
	}
	if entry.Type != "task" {
		t.Errorf("index entry type = %q, want task", entry.Type)
	}
}

func TestGetReturnsRecord(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := ops.Get(root, "plans.demo-1", "", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !contains(string(res.Bytes), "[plans.demo-1]") {
		t.Errorf("Get bytes missing bracket header; got:\n%s", res.Bytes)
	}
}

func TestUpdateMergesFields(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := ops.Update(root, "plans.demo-1", "", map[string]any{
		"status": "done",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	res, _, err := ops.GetAllFields(root, "plans.demo-1", "")
	if err != nil {
		t.Fatalf("GetAllFields: %v", err)
	}
	if got := res.Fields["status"]; got != "done" {
		t.Errorf("status = %v, want done", got)
	}
	if got := res.Fields["title"]; got != "first" {
		t.Errorf("title = %v, want first (preserved)", got)
	}
}

func TestDeleteRemovesRecordAndIndex(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := ops.Delete(root, "plans.demo-1", ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	idx, _ := index.Load(root)
	if _, ok := idx.Get("plans.demo-1"); ok {
		t.Error("index entry survived delete")
	}
	if _, err := ops.Get(root, "plans.demo-1", "", nil); err == nil {
		t.Error("expected error after delete")
	}
}

func TestIsScopeAddress(t *testing.T) {
	root := withSingleFileSchema(t)
	cases := []struct {
		id      string
		isScope bool
	}{
		{"plans", true},
		{"plans.demo-1", false},
	}
	for _, tc := range cases {
		got, err := ops.IsScopeAddress(root, tc.id)
		if err != nil {
			t.Errorf("IsScopeAddress(%q): %v", tc.id, err)
			continue
		}
		if got != tc.isScope {
			t.Errorf("IsScopeAddress(%q) = %v, want %v", tc.id, got, tc.isScope)
		}
	}
}

func TestUnknownIDFailsLoudly(t *testing.T) {
	root := withSingleFileSchema(t)
	_, err := ops.Get(root, "nope.x", "", nil)
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
