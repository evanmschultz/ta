package schema

import (
	"os"
	"testing"
)

// TestSchemaLoad_TemplateManifest locks the structural contract of the
// binary-shipped template_manifest schema: the db must load cleanly,
// the three concrete manifest types (html, md, txt) must all be
// present with no auto_spawn (F23 OFF), and each type's resolved
// `format` field must carry a single-element enum naming exactly that
// type.
func TestSchemaLoad_TemplateManifest(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/template_manifest.toml")
	if err != nil {
		t.Fatalf("read template_manifest schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load template_manifest schema: %v", err)
	}

	db, ok := reg.DBs["template_manifest"]
	if !ok {
		t.Fatal("template_manifest db missing from registry")
	}

	for _, typeName := range []string{"html", "md", "txt"} {
		t.Run(typeName, func(t *testing.T) {
			st, ok := db.Types[typeName]
			if !ok {
				t.Fatalf("template_manifest.%s type missing", typeName)
			}
			if got := len(st.AutoSpawn); got != 0 {
				t.Errorf("template_manifest.%s AutoSpawn len = %d, want 0 (F23 OFF)", typeName, got)
			}
			formatField, ok := st.Fields["format"]
			if !ok {
				t.Fatalf("template_manifest.%s missing required field %q", typeName, "format")
			}
			if got := len(formatField.Enum); got != 1 {
				t.Fatalf("template_manifest.%s format enum len = %d, want 1", typeName, got)
			}
			got, ok := formatField.Enum[0].(string)
			if !ok {
				t.Fatalf("template_manifest.%s format enum[0] type = %T, want string", typeName, formatField.Enum[0])
			}
			if got != typeName {
				t.Errorf("template_manifest.%s format enum[0] = %q, want %q", typeName, got, typeName)
			}
		})
	}
}

// TestSchemaValidate_TemplateManifest_SampleRecord locks the validation
// contract: a minimal sample payload for each concrete manifest type
// (carrying only the required NodeBase fields + the leaf-required
// `format`) must validate cleanly via Registry.Validate.
func TestSchemaValidate_TemplateManifest_SampleRecord(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/template_manifest.toml")
	if err != nil {
		t.Fatalf("read template_manifest schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load template_manifest schema: %v", err)
	}

	cases := []struct {
		typeName string
		format   string
	}{
		{"html", "html"},
		{"md", "md"},
		{"txt", "txt"},
	}
	for _, tc := range cases {
		t.Run(tc.typeName, func(t *testing.T) {
			sample := map[string]any{
				"title":      "Sample " + tc.typeName + " manifest",
				"state":      "todo",
				"created_at": "2026-05-15T00:00:00Z",
				"updated_at": "2026-05-15T00:00:00Z",
				"format":     tc.format,
			}
			if err := reg.Validate("template_manifest."+tc.typeName, sample); err != nil {
				t.Errorf("Validate(template_manifest.%s) returned %v, want nil", tc.typeName, err)
			}
		})
	}
}
