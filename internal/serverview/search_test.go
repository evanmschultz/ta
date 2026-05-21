package serverview_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/serverview"
)

// TestServeView_LoadSearchEmptyQuery validates that LoadSearch rejects empty query.
func TestServeView_LoadSearchEmptyQuery(t *testing.T) {
	root := t.TempDir()
	_, err := serverview.LoadSearch(root, "")
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

// TestServeView_LoadSearchNoResults validates that LoadSearch returns empty results for non-matching query.
func TestServeView_LoadSearchNoResults(t *testing.T) {
	root := t.TempDir()
	setupSchema(t, root)

	result, err := serverview.LoadSearch(root, "nonexistent_pattern_xyz_abc_123")
	if err != nil {
		t.Fatalf("LoadSearch: %v", err)
	}

	if result.TemplateName != "search_results.html" {
		t.Errorf("TemplateName: expected search_results.html, got %q", result.TemplateName)
	}

	if len(result.Results) != 0 {
		t.Errorf("Results: expected 0 results, got %d", len(result.Results))
	}
}

// TestServeView_LoadSearchTemplate validates that LoadSearch returns correct template name.
func TestServeView_LoadSearchTemplate(t *testing.T) {
	root := t.TempDir()
	setupSchema(t, root)

	result, err := serverview.LoadSearch(root, "plans")
	if err != nil {
		t.Fatalf("LoadSearch: %v", err)
	}

	if result.TemplateName != "search_results.html" {
		t.Errorf("TemplateName: expected search_results.html, got %q", result.TemplateName)
	}
}

// setupSchema creates a minimal .ta project with a plans schema for testing.
func setupSchema(t *testing.T, root string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	schema := `[plans]
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
`
	schemaPath := filepath.Join(root, ".ta", "schema.toml")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	// Reset cache
	ops.ResetDefaultCacheForTest()
}
