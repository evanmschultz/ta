package schema

import (
	"os"
	"testing"
)

// TestSchemaLoad_HtmlTemplate locks the html_template schema's overall
// shape: the db registers, every concrete view type is present, each
// inherits the View base's required fields, and none of them carry an
// auto_spawn block (F23 is OFF for html_template).
func TestSchemaLoad_HtmlTemplate(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/html_template.toml")
	if err != nil {
		t.Fatalf("read html_template schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load html_template schema: %v", err)
	}
	db, ok := reg.DBs["html_template"]
	if !ok {
		t.Fatal("html_template db missing from registry")
	}

	wantTypes := []string{
		"cascade_drop_view",
		"cascade_planner_view",
		"cascade_droplet_view",
		"cascade_qa_view",
		"roadmap_version_view",
		"schema_browser_view",
	}
	for _, name := range wantTypes {
		t.Run(name, func(t *testing.T) {
			st, ok := db.Types[name]
			if !ok {
				t.Fatalf("html_template.%s type missing", name)
			}
			for _, f := range []string{"view_name", "template_relpath", "variant"} {
				if _, ok := st.Fields[f]; !ok {
					t.Errorf("html_template.%s missing inherited field %q from View base", name, f)
				}
			}
			if got := len(st.AutoSpawn); got != 0 {
				t.Errorf("html_template.%s AutoSpawn len = %d, want 0 (F23 OFF for html_template)", name, got)
			}
		})
	}
}

// TestSchemaValidate_HtmlTemplate_SampleRecord builds a minimal record
// for html_template.cascade_drop_view filled with the required fields
// from NodeBase + View and asserts the registry validates it.
func TestSchemaValidate_HtmlTemplate_SampleRecord(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/html_template.toml")
	if err != nil {
		t.Fatalf("read html_template schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load html_template schema: %v", err)
	}

	sample := map[string]any{
		"title":            "Cascade drop view",
		"state":            "todo",
		"created_at":       "2026-05-15T00:00:00Z",
		"updated_at":       "2026-05-15T00:00:00Z",
		"view_name":        "cascade_drop",
		"template_relpath": "views/cascade_drop.html",
	}
	if err := reg.Validate("html_template.cascade_drop_view", sample); err != nil {
		t.Fatalf("validate sample html_template.cascade_drop_view: %v", err)
	}
}

// TestSchemaLoad_HtmlTemplate_AllSixViewsDeclared locks the exact 6
// view types the html_template schema must expose. Drift here — a
// renamed view, a missing one, a stray seventh — fails CI loudly.
func TestSchemaLoad_HtmlTemplate_AllSixViewsDeclared(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/html_template.toml")
	if err != nil {
		t.Fatalf("read html_template schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load html_template schema: %v", err)
	}
	db, ok := reg.DBs["html_template"]
	if !ok {
		t.Fatal("html_template db missing from registry")
	}

	expected := []string{
		"cascade_drop_view",
		"cascade_planner_view",
		"cascade_droplet_view",
		"cascade_qa_view",
		"roadmap_version_view",
		"schema_browser_view",
	}
	for _, name := range expected {
		if _, ok := db.Types[name]; !ok {
			t.Errorf("html_template.%s missing — schema must declare all six view types", name)
		}
	}
}
