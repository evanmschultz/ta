package serverview_test

import (
	"os"
	"testing"

	"github.com/evanmschultz/ta/internal/serverview"
)

// TestServeView_LoadSchema validates LoadSchema error handling on empty projects.
func TestServeView_LoadSchema(t *testing.T) {
	root := t.TempDir()
	result, err := serverview.LoadSchema(root)

	if err == nil {
		t.Fatal("expected error for empty project")
	}

	if result.TemplateName != "" {
		t.Errorf("TemplateName: expected empty, got %q", result.TemplateName)
	}

	if len(result.Scopes) != 0 {
		t.Errorf("Scopes: expected empty, got %d scopes", len(result.Scopes))
	}
}

// TestServeView_LoadSchemaWithData validates LoadSchema parses schema structure correctly.
func TestServeView_LoadSchemaWithData(t *testing.T) {
	root := t.TempDir()

	// Create a minimal .ta/schema.toml for testing.
	err := setup(root)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := serverview.LoadSchema(root)
	if err != nil {
		t.Fatalf("LoadSchema failed: %v", err)
	}

	// Validate template name.
	if result.TemplateName != "schema_browser.html" {
		t.Errorf("TemplateName: expected %q, got %q", "schema_browser.html", result.TemplateName)
	}

	// Validate that scopes were extracted.
	if len(result.Scopes) == 0 {
		t.Fatal("Scopes: expected at least one scope")
	}

	// Verify cascade scope exists (minimal schema should have it).
	cascadeScope := findScope(result.Scopes, "cascade")
	if cascadeScope == nil {
		t.Fatal("Scopes: expected cascade scope to exist")
	}

	// Verify that cascade scope has types.
	if len(cascadeScope.Types) == 0 {
		t.Fatal("cascade scope: expected at least one type")
	}

	// Verify cascade.drop type exists.
	dropType := findType(cascadeScope.Types, "drop")
	if dropType == nil {
		t.Fatal("cascade scope: expected drop type to exist")
	}

	// Verify drop type has fields.
	if len(dropType.Fields) == 0 {
		t.Fatal("cascade.drop type: expected at least one field")
	}

	// Verify drop_number field exists (from schema).
	dropNumberField := findField(dropType.Fields, "drop_number")
	if dropNumberField == nil {
		t.Fatal("cascade.drop type: expected drop_number field to exist")
	}

	// Validate field properties.
	if dropNumberField.Type != "integer" {
		t.Errorf("drop_number field type: expected %q, got %q", "integer", dropNumberField.Type)
	}
	if !dropNumberField.Required {
		t.Error("drop_number field: expected required=true")
	}
}

func setup(root string) error {
	// Create .ta/ directory.
	taDir := root + "/.ta"
	if err := os.MkdirAll(taDir, 0755); err != nil {
		return err
	}

	// Write minimal schema.toml.
	schemaContent := `[cascade]
description = "Cascade trees — drops + their segments, confluences, droplets, planners, QA records, failures."

[cascade.drop]
description = "L1 cascade root — one self-contained unit of work."
extends = "ActionItem"

[cascade.drop.fields]
[cascade.drop.fields.drop_number]
description = "Sequential drop number (matches the drop_N directory)."
required = true
type = "integer"

[cascade.drop.fields.structural_type]
enum = ["drop"]
required = true
type = "string"
`

	schemaPath := taDir + "/schema.toml"
	return os.WriteFile(schemaPath, []byte(schemaContent), 0644)
}

func findScope(scopes []serverview.ScopeView, name string) *serverview.ScopeView {
	for i := range scopes {
		if scopes[i].Name == name {
			return &scopes[i]
		}
	}
	return nil
}

func findType(types []serverview.TypeView, name string) *serverview.TypeView {
	for i := range types {
		if types[i].Name == name {
			return &types[i]
		}
	}
	return nil
}

func findField(fields []serverview.FieldView, name string) *serverview.FieldView {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

