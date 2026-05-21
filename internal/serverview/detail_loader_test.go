package serverview_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/serverview"
)

// TestServeView_LoadDetail_Basic verifies LoadDetail can be called.
func TestServeView_LoadDetail_Basic(t *testing.T) {
	root := t.TempDir()
	result, err := serverview.LoadDetail(root, "drop_001.drop.test")

	// Should error on empty project
	if err == nil {
		t.Fatal("expected error for empty project")
	}

	// Result should be zero on error
	if result.TemplateName != "" {
		t.Errorf("TemplateName: expected empty, got %q", result.TemplateName)
	}
}

// writeDetailSchema writes a test schema with all cascade types.
func writeDetailSchema(t *testing.T, root string) {
	t.Helper()

	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatal(err)
	}

	schemaPath := filepath.Join(taDir, "schema.toml")
	schemaContents := `
[cascade]
description = "Cascade types for testing"
paths = [".ta/cascade/drops/*.toml"]

[cascade.drop]
description = "Cascade drop record"
[cascade.drop.fields.title]
type = "string"
required = true
[cascade.drop.fields.role]
type = "string"
required = true
[cascade.drop.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete", "failed"]
[cascade.drop.fields.drop_number]
type = "integer"
required = true
[cascade.drop.fields.created_at]
type = "datetime"
required = true
[cascade.drop.fields.updated_at]
type = "datetime"
required = true
[cascade.drop.fields.structural_type]
type = "string"
required = true
enum = ["drop"]
[cascade.drop.fields.objective]
type = "string"
[cascade.drop.fields.parent_id]
type = "string"

[cascade.planner]
description = "Cascade planner record"
[cascade.planner.fields.title]
type = "string"
required = true
[cascade.planner.fields.role]
type = "string"
required = true
[cascade.planner.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete", "failed"]
[cascade.planner.fields.drop_number]
type = "integer"
required = true
[cascade.planner.fields.created_at]
type = "datetime"
required = true
[cascade.planner.fields.updated_at]
type = "datetime"
required = true
[cascade.planner.fields.structural_type]
type = "string"
required = true
enum = ["planner"]

[cascade.droplet]
description = "Cascade droplet record"
[cascade.droplet.fields.title]
type = "string"
required = true
[cascade.droplet.fields.role]
type = "string"
required = true
[cascade.droplet.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete", "failed"]
[cascade.droplet.fields.drop_number]
type = "integer"
required = true
[cascade.droplet.fields.created_at]
type = "datetime"
required = true
[cascade.droplet.fields.updated_at]
type = "datetime"
required = true
[cascade.droplet.fields.structural_type]
type = "string"
required = true
enum = ["droplet"]

[cascade.qa_proof]
description = "Cascade QA proof record"
[cascade.qa_proof.fields.title]
type = "string"
required = true
[cascade.qa_proof.fields.role]
type = "string"
required = true
[cascade.qa_proof.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete", "failed"]
[cascade.qa_proof.fields.drop_number]
type = "integer"
required = true
[cascade.qa_proof.fields.created_at]
type = "datetime"
required = true
[cascade.qa_proof.fields.updated_at]
type = "datetime"
required = true
[cascade.qa_proof.fields.structural_type]
type = "string"
required = true
enum = ["qa_proof"]
[cascade.qa_proof.fields.target_id]
type = "string"
required = true

[cascade.qa_falsification]
description = "Cascade QA falsification record"
[cascade.qa_falsification.fields.title]
type = "string"
required = true
[cascade.qa_falsification.fields.role]
type = "string"
required = true
[cascade.qa_falsification.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete", "failed"]
[cascade.qa_falsification.fields.drop_number]
type = "integer"
required = true
[cascade.qa_falsification.fields.created_at]
type = "datetime"
required = true
[cascade.qa_falsification.fields.updated_at]
type = "datetime"
required = true
[cascade.qa_falsification.fields.structural_type]
type = "string"
required = true
enum = ["qa_falsification"]
[cascade.qa_falsification.fields.target_id]
type = "string"
required = true
`

	if err := os.WriteFile(schemaPath, []byte(schemaContents), 0o644); err != nil {
		t.Fatal(err)
	}

	ops.SwapDefaultCacheLoaderForTest(func(p string) (config.Resolution, error) {
		return config.Resolve(p)
	})
}

// writeDetailRecord writes a cascade record to the test project.
func writeDetailRecord(t *testing.T, root, id, content string) {
	t.Helper()

	dropsDir := filepath.Join(root, ".ta", "cascade", "drops")
	if err := os.MkdirAll(dropsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	filePath := filepath.Join(dropsDir, id+".toml")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestServeView_LoadDetail_AllTypes tests loading all cascade types.
func TestServeView_LoadDetail_AllTypes(t *testing.T) {
	tests := []struct {
		name              string
		structuralType    string
		expectedTemplate  string
		recordContent     string
	}{
		{
			name:             "drop",
			structuralType:   "drop",
			expectedTemplate: "cascade_drop.html",
			recordContent: `["drop_008.drop.test"]
drop_number = 8
title = "Test Drop"
role = "planner"
state = "in_progress"
structural_type = "drop"
created_at = "2026-05-21T00:00:00Z"
updated_at = "2026-05-21T00:00:00Z"
`,
		},
		{
			name:             "planner",
			structuralType:   "planner",
			expectedTemplate: "cascade_planner.html",
			recordContent: `["drop_008.drop.planner"]
drop_number = 8
title = "Test Planner"
role = "planner"
state = "complete"
structural_type = "planner"
created_at = "2026-05-21T00:00:00Z"
updated_at = "2026-05-21T00:00:00Z"
`,
		},
		{
			name:             "droplet",
			structuralType:   "droplet",
			expectedTemplate: "cascade_droplet.html",
			recordContent: `["drop_008.drop.droplet"]
drop_number = 8
title = "Test Droplet"
role = "builder"
state = "in_progress"
structural_type = "droplet"
created_at = "2026-05-21T00:00:00Z"
updated_at = "2026-05-21T00:00:00Z"
`,
		},
		{
			name:             "qa_proof",
			structuralType:   "qa_proof",
			expectedTemplate: "cascade_qa.html",
			recordContent: `["drop_008.drop.qa-proof"]
drop_number = 8
title = "Test QA Proof"
role = "qa-proof"
state = "in_progress"
structural_type = "qa_proof"
target_id = "drop_008.drop.test"
created_at = "2026-05-21T00:00:00Z"
updated_at = "2026-05-21T00:00:00Z"
`,
		},
		{
			name:             "qa_falsification",
			structuralType:   "qa_falsification",
			expectedTemplate: "cascade_qa.html",
			recordContent: `["drop_008.drop.qa-falsif"]
drop_number = 8
title = "Test QA Falsification"
role = "qa-falsification"
state = "todo"
structural_type = "qa_falsification"
target_id = "drop_008.drop.test"
created_at = "2026-05-21T00:00:00Z"
updated_at = "2026-05-21T00:00:00Z"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeDetailSchema(t, root)

			id := "drop_008.drop." + tt.name
			writeDetailRecord(t, root, id, tt.recordContent)

			result, err := serverview.LoadDetail(root, id)
			if err != nil {
				t.Fatalf("LoadDetail: %v", err)
			}

			if result.TemplateName != tt.expectedTemplate {
				t.Errorf("TemplateName: got %q, want %q", result.TemplateName, tt.expectedTemplate)
			}
		})
	}
}

// TestServeView_LoadDetail_ErrorCases tests error handling.
func TestServeView_LoadDetail_ErrorCases(t *testing.T) {
	t.Run("MissingRecord", func(t *testing.T) {
		root := t.TempDir()
		writeDetailSchema(t, root)

		result, err := serverview.LoadDetail(root, "drop_999.drop.missing")
		if err == nil {
			t.Fatal("expected error for missing record")
		}
		if result.ID != "" || result.TemplateName != "" {
			t.Errorf("expected zero values on error")
		}
	})

	t.Run("UnknownStructuralType", func(t *testing.T) {
		root := t.TempDir()
		writeDetailSchema(t, root)

		writeDetailRecord(t, root, "drop_008.drop.unknown", `["drop_008.drop.unknown"]
drop_number = 8
title = "Unknown"
role = "unknown"
state = "todo"
structural_type = "unknown_type"
created_at = "2026-05-21T00:00:00Z"
updated_at = "2026-05-21T00:00:00Z"
`)

		result, err := serverview.LoadDetail(root, "drop_008.drop.unknown")
		if err == nil {
			t.Fatal("expected error for unknown structural_type")
		}
		if result.TemplateName != "" {
			t.Errorf("expected empty template on error")
		}
	})

	t.Run("MissingStructuralType", func(t *testing.T) {
		root := t.TempDir()
		writeDetailSchema(t, root)

		writeDetailRecord(t, root, "drop_008.drop.no-type", `["drop_008.drop.no-type"]
drop_number = 8
title = "No Type"
role = "planner"
state = "todo"
created_at = "2026-05-21T00:00:00Z"
updated_at = "2026-05-21T00:00:00Z"
`)

		result, err := serverview.LoadDetail(root, "drop_008.drop.no-type")
		if err == nil {
			t.Fatal("expected error for missing structural_type")
		}
		if result.TemplateName != "" {
			t.Errorf("expected empty template on error")
		}
	})
}
