package schema

import (
	"strings"
	"testing"
)

func TestMetaSchemaEmbeddedAndNonEmpty(t *testing.T) {
	if MetaSchemaTOML == "" {
		t.Fatal("MetaSchemaTOML is empty; embed directive is broken")
	}
	if !strings.Contains(MetaSchemaTOML, "[ta_schema]") {
		t.Errorf("MetaSchemaTOML missing [ta_schema] root: %s", MetaSchemaTOML[:min(200, len(MetaSchemaTOML))])
	}
}

func TestMetaSchemaLoadsUnderNewGrammar(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema must load under its own grammar: %v", err)
	}
	db, ok := reg.DBs["ta_schema"]
	if !ok {
		t.Fatal("ta_schema db missing from parsed meta-schema")
	}
	for _, want := range []string{"db", "type", "field"} {
		if _, ok := db.Types[want]; !ok {
			t.Errorf("meta-schema missing kind %q", want)
		}
	}
}

func TestMetaSchemaPathConstant(t *testing.T) {
	if MetaSchemaPath != "ta_schema" {
		t.Errorf("MetaSchemaPath = %q, want ta_schema", MetaSchemaPath)
	}
}

// TestMetaSchemaDeclaresElementGrammar locks the F21 contract on the
// meta-schema: the field kind must declare element_type and
// element_fields sub-fields, and a [ta_schema.types] documentation
// block must be present.
func TestMetaSchemaDeclaresElementGrammar(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema load: %v", err)
	}
	field, ok := reg.DBs["ta_schema"].Types["field"]
	if !ok {
		t.Fatal("missing ta_schema.field kind")
	}
	if _, ok := field.Fields["element_type"]; !ok {
		t.Error("ta_schema.field.fields.element_type missing — F21 grammar must be declared")
	}
	if _, ok := field.Fields["element_fields"]; !ok {
		t.Error("ta_schema.field.fields.element_fields missing — F21 grammar must be declared")
	}
}

// TestMetaSchemaValidatesUserElementTypeSchema confirms a user schema
// using element_type / element_fields / [<db>.types.<alias>] loads
// cleanly under the F21 grammar.
func TestMetaSchemaValidatesUserElementTypeSchema(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.ChecklistItem]
description = "x"

[plans.types.ChecklistItem.fields.id]
type = "string"
required = true

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.paths]
type = "array"
element_type = "string"

[plans.task.fields.checklist]
type = "array"
element_type = "ChecklistItem"
`
	if _, err := LoadBytes([]byte(src)); err != nil {
		t.Fatalf("user schema using F21 grammar should load: %v", err)
	}
}
