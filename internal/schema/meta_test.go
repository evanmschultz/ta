package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/backend/md"
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

// TestMetaSchemaDeclaresExtendsAndBases locks the F22 contract on the
// meta-schema: a [ta_schema.base] kind block must be present and
// [ta_schema.type] must declare an extends sub-field. The
// [ta_schema.bases] documentation block must also be present (parallel
// to [ta_schema.types]).
func TestMetaSchemaDeclaresExtendsAndBases(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema load: %v", err)
	}
	tsDB, ok := reg.DBs["ta_schema"]
	if !ok {
		t.Fatal("ta_schema db missing")
	}
	baseKind, ok := tsDB.Types["base"]
	if !ok {
		t.Fatal("ta_schema.base kind missing — F22 grammar must be declared as a first-class kind")
	}
	if _, ok := baseKind.Fields["extends"]; !ok {
		t.Error("ta_schema.base.fields.extends missing — bases must declare extends as inheritable")
	}
	typeKind, ok := tsDB.Types["type"]
	if !ok {
		t.Fatal("ta_schema.type kind missing")
	}
	if _, ok := typeKind.Fields["extends"]; !ok {
		t.Error("ta_schema.type.fields.extends missing — concrete types must declare extends as a recognized key")
	}
}

// TestMetaSchemaValidatesUserExtendsSchema confirms a user schema
// using bases + extends loads cleanly under the F22 grammar.
func TestMetaSchemaValidatesUserExtendsSchema(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.NodeBase]
description = "Common cascade-node fields."

[plans.bases.NodeBase.fields.parent_id]
type = "string"

[plans.bases.NodeBase.fields.title]
type = "string"
required = true

[plans.bases.ActionItem]
extends = "NodeBase"

[plans.bases.ActionItem.fields.role]
type = "string"
required = true

[plans.task]
description = "x"
extends = "ActionItem"

[plans.task.fields.status]
type = "string"
required = true
`
	if _, err := LoadBytes([]byte(src)); err != nil {
		t.Fatalf("user schema using F22 grammar should load: %v", err)
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

// TestMetaSchema_SelfHostsNestedFieldsKey locks the F28 contract on the
// TestExamplesCascadeRoundTrips locks the binary-shipped cascade
// schema (`examples/schemas/cascade.toml`) against the loader so any
// future drift — missing required descriptions, retired meta-fields,
// non-canonical syntax — fails CI loudly instead of surfacing only
// when a user runs `ta init`. Same lock for `examples/schemas/agents.toml`.
// Path resolution is relative to the test's working dir
// (`internal/schema/`), reaching up two levels to the repo root.
func TestExamplesLoadCleanly(t *testing.T) {
	for _, rel := range []string{
		"../../examples/schemas/cascade.toml",
		"../../examples/schemas/agents.toml",
	} {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			data, err := os.ReadFile(rel)
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			if _, err := LoadBytes(data); err != nil {
				t.Errorf("examples schema %s must load cleanly: %v", rel, err)
			}
		})
	}
}

// TestExamplesAgentsRoundTrip locks the F34 contract on the binary-shipped
// agent files: every `examples/agents/ta/*.md` file must be a valid
// `agents_home.agent` record under the `agents.toml` schema. The file
// must split cleanly into frontmatter + body via md.SplitFrontmatter,
// the frontmatter must decode into a typed map via md.DecodeFrontmatter,
// and the resulting fields (with the body folded into `prompt` per the
// `body_field` declaration) must validate against the schema's `agent`
// type. Drift here — missing required fields, unknown frontmatter keys,
// empty bodies, name/stem mismatches — fails CI loudly instead of
// surfacing only when a user runs `ta init` against the binary library.
//
// Path resolution is relative to the test's working dir
// (`internal/schema/`), reaching up two levels to the repo root.
func TestExamplesAgentsRoundTrip(t *testing.T) {
	schemaBytes, err := os.ReadFile("../../examples/schemas/agents.toml")
	if err != nil {
		t.Fatalf("read agents schema: %v", err)
	}
	reg, err := LoadBytes(schemaBytes)
	if err != nil {
		t.Fatalf("load agents schema: %v", err)
	}
	if _, ok := reg.DBs["agents_home"]; !ok {
		t.Fatal("agents_home db missing — F34 schema regressed")
	}
	if _, ok := reg.DBs["agents_project"]; !ok {
		t.Fatal("agents_project db missing — F34 schema regressed")
	}

	matches, err := filepath.Glob("../../examples/agents/ta/*.md")
	if err != nil {
		t.Fatalf("glob agents: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no agent files found under examples/agents/ta/ — F34 ships at least one")
	}

	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			buf, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			front, body, err := md.SplitFrontmatter(buf)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			if len(front) == 0 {
				t.Fatal("missing frontmatter — F34 invariant: every shipped agent has a frontmatter fence")
			}
			if len(body) == 0 {
				t.Fatal("empty body — F34 invariant: prompt body must be non-empty")
			}
			fields, err := md.DecodeFrontmatter(front)
			if err != nil {
				t.Fatalf("DecodeFrontmatter: %v", err)
			}
			fields["prompt"] = string(body)

			if err := reg.Validate("agents_home.agent", fields); err != nil {
				t.Errorf("validate against agents_home.agent: %v", err)
			}
			if err := reg.Validate("agents_project.agent", fields); err != nil {
				t.Errorf("validate against agents_project.agent: %v", err)
			}

			stem := strings.TrimSuffix(filepath.Base(path), ".md")
			if got, _ := fields["name"].(string); got != stem {
				t.Errorf("frontmatter name %q does not match filename stem %q (Claude Code requires sync)", got, stem)
			}
		})
	}
}

// meta-schema: the field kind must declare a `fields` sub-field and that
// declaration must itself parse cleanly under the new grammar (self-host
// check).
func TestMetaSchema_SelfHostsNestedFieldsKey(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema load: %v", err)
	}
	field, ok := reg.DBs["ta_schema"].Types["field"]
	if !ok {
		t.Fatal("missing ta_schema.field kind")
	}
	fieldsDecl, ok := field.Fields["fields"]
	if !ok {
		t.Fatal("ta_schema.field.fields.fields missing — F28 grammar must be declared on the meta-schema")
	}
	if fieldsDecl.Type != TypeTable {
		t.Errorf("ta_schema.field.fields.fields.Type = %q, want %q", fieldsDecl.Type, TypeTable)
	}
}
