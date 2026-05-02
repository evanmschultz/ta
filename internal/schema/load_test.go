package schema

import (
	"errors"
	"strings"
	"testing"
)

// exampleConfig is a TOML db with four fields used by tests in this
// package. Per F10 (PLAN §12.17.9) `format` is NOT a meta-field; it
// is inferred from the path extension.
const exampleConfig = `
[plans]
paths = ["plans.toml"]
description = "Example planning db for schema tests."

[plans.task]
description = "A unit of work an agent picks up"

[plans.task.fields.id]
type = "string"
required = true
description = "Stable identifier, e.g. 'TASK-001'"

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "in_progress", "done", "blocked"]
description = "Current state of the task"

[plans.task.fields.body]
type = "string"
required = false
format = "markdown"
description = "Freeform writeup."

[plans.task.fields.estimate_hours]
type = "integer"
required = false
description = "Rough hour estimate"
`

func TestLoadHappyPath(t *testing.T) {
	reg, err := Load(strings.NewReader(exampleConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db, ok := reg.DBs["plans"]
	if !ok {
		t.Fatal("missing plans db")
	}
	if len(db.Paths) != 1 || db.Paths[0] != "plans.toml" {
		t.Errorf("paths = %v, want [\"plans.toml\"]", db.Paths)
	}
	if db.Format != FormatTOML {
		t.Errorf("format = %q, want toml (inferred from .toml extension)", db.Format)
	}
	task, ok := db.Types["task"]
	if !ok {
		t.Fatal("missing task section type")
	}
	if task.Description != "A unit of work an agent picks up" {
		t.Errorf("description = %q", task.Description)
	}
	if got := len(task.Fields); got != 4 {
		t.Errorf("field count = %d, want 4", got)
	}
}

func TestLoadRejectsFormatKey(t *testing.T) {
	// F10: format is inferred from path extension; `format = "..."`
	// at the db level is an unknown meta-field that must be rejected.
	src := `
[plans]
paths = ["plans.toml"]
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for `format` meta-key under F10")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error should mention `format`: %v", err)
	}
}

func TestLoadInfersFormatFromTOMLExtension(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.DBs["plans"].Format != FormatTOML {
		t.Errorf("format = %q, want toml", reg.DBs["plans"].Format)
	}
}

func TestLoadInfersFormatFromMDExtension(t *testing.T) {
	src := `
[docs]
paths = ["docs/*.md"]

[docs.section]
description = "An MD section"
heading = 2

[docs.section.fields.body]
type = "string"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.DBs["docs"].Format != FormatMD {
		t.Errorf("format = %q, want md", reg.DBs["docs"].Format)
	}
}

func TestLoadRejectsExtensionlessPath(t *testing.T) {
	src := `
[plans]
paths = ["plans"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for extensionless path")
	}
	if !errors.Is(err, ErrAmbiguousPathFormat) {
		t.Errorf("err = %v, want ErrAmbiguousPathFormat", err)
	}
}

func TestLoadRejectsCollectionMount(t *testing.T) {
	src := `
[docs]
paths = ["docs/"]

[docs.section]
description = "An MD section"
heading = 2

[docs.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for trailing-slash collection mount")
	}
	if !errors.Is(err, ErrCollectionMountUnsupported) {
		t.Errorf("err = %v, want ErrCollectionMountUnsupported", err)
	}
}

func TestLoadRejectsDotProjectRootMount(t *testing.T) {
	src := `
[anyfile]
paths = ["."]

[anyfile.section]
description = "Catch-all"
heading = 1

[anyfile.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for `.` project-root mount")
	}
	if !errors.Is(err, ErrCollectionMountUnsupported) {
		t.Errorf("err = %v, want ErrCollectionMountUnsupported", err)
	}
}

func TestLoadRejectsInconsistentExtensions(t *testing.T) {
	src := `
[mixed]
paths = ["a.toml", "b.md"]

[mixed.thing]
description = "Mixed"

[mixed.thing.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for mixed extensions")
	}
	if !errors.Is(err, ErrInconsistentPathFormats) {
		t.Errorf("err = %v, want ErrInconsistentPathFormats", err)
	}
}

func TestLoadRejectsUnsupportedFieldType(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.weird]
type = "color"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unsupported field type")
	}
}

func TestLoadRequiresPaths(t *testing.T) {
	src := `
[plans]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for missing paths")
	}
}

// ----------------------------------------------------------------------------
// F21 — typed array elements + element_fields + type aliases
// ----------------------------------------------------------------------------

func TestLoadElementTypePrimitive(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.paths]
type = "array"
element_type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := reg.DBs["plans"].Types["task"].Fields["paths"]
	if f.ElementType != TypeString {
		t.Errorf("ElementType = %q, want string", f.ElementType)
	}
	if len(f.ElementFields) != 0 {
		t.Errorf("ElementFields = %v, want empty", f.ElementFields)
	}
}

func TestLoadElementTypeRejectsUnknownPrimitive(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.paths]
type = "array"
element_type = "color"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown element_type")
	}
	if !errors.Is(err, ErrUnknownElementType) {
		t.Errorf("err = %v, want ErrUnknownElementType", err)
	}
}

func TestLoadElementTypeOnNonArrayRejected(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
element_type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for element_type on non-array field")
	}
	if !strings.Contains(err.Error(), "element_type is only valid on type = \"array\"") {
		t.Errorf("error should mention non-array rejection: %v", err)
	}
}

func TestLoadElementTypeRejectsArrayOfArrays(t *testing.T) {
	// element_type = "array" is rejected outright in v1; nested
	// arrays of arrays would require a recursive element walker the
	// load contract has no syntax for.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.matrix]
type = "array"
element_type = "array"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for element_type = \"array\"")
	}
	if !errors.Is(err, ErrUnknownElementType) {
		t.Errorf("err = %v, want ErrUnknownElementType", err)
	}
	if !strings.Contains(err.Error(), "nested arrays") {
		t.Errorf("error should mention nested arrays: %v", err)
	}
}

func TestLoadElementFieldsRequiresTableElement(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.things]
type = "array"
element_type = "string"

[plans.task.fields.things.element_fields.id]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for element_fields without element_type=table")
	}
	if !strings.Contains(err.Error(), "element_fields requires element_type = \"table\"") {
		t.Errorf("error should explain the requirement: %v", err)
	}
}

func TestLoadElementFieldsNested(t *testing.T) {
	// matrix: array of tables; each table has cells: array of strings.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.matrix]
type = "array"
element_type = "table"

[plans.task.fields.matrix.element_fields.row_id]
type = "string"
required = true

[plans.task.fields.matrix.element_fields.cells]
type = "array"
element_type = "string"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := reg.DBs["plans"].Types["task"].Fields["matrix"]
	if f.ElementType != TypeTable {
		t.Errorf("ElementType = %q, want table", f.ElementType)
	}
	cells, ok := f.ElementFields["cells"]
	if !ok {
		t.Fatal("missing cells sub-field")
	}
	if cells.Type != TypeArray || cells.ElementType != TypeString {
		t.Errorf("cells: type=%q element_type=%q", cells.Type, cells.ElementType)
	}
}

func TestLoadAliasBasic(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.ChecklistItem]
description = "A checklist line item."

[plans.types.ChecklistItem.fields.id]
type = "string"
required = true

[plans.types.ChecklistItem.fields.text]
type = "string"
required = true

[plans.task]
description = "x"

[plans.task.fields.start_criteria]
type = "array"
element_type = "ChecklistItem"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := reg.DBs["plans"].Types["task"].Fields["start_criteria"]
	if f.ElementType != TypeTable {
		t.Errorf("ElementType after inlining = %q, want table", f.ElementType)
	}
	if _, ok := f.ElementFields["id"]; !ok {
		t.Errorf("inlined alias missing id sub-field; got %v", f.ElementFields)
	}
	if _, ok := f.ElementFields["text"]; !ok {
		t.Errorf("inlined alias missing text sub-field; got %v", f.ElementFields)
	}
	// `types` is the alias namespace, NOT a record type, so it must
	// not surface as a SectionType.
	if _, isType := reg.DBs["plans"].Types["types"]; isType {
		t.Error("`types` leaked as a record type")
	}
}

func TestLoadAliasCycleSelf(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.A]
description = "x"

[plans.types.A.fields.next]
type = "array"
element_type = "A"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected cycle error for self-referential alias")
	}
	if !errors.Is(err, ErrAliasCycle) {
		t.Errorf("err = %v, want ErrAliasCycle", err)
	}
}

func TestLoadAliasCycleMutual(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.A]
description = "x"

[plans.types.A.fields.toB]
type = "array"
element_type = "B"

[plans.types.B]
description = "x"

[plans.types.B.fields.toA]
type = "array"
element_type = "A"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected cycle error for mutual alias chain")
	}
	if !errors.Is(err, ErrAliasCycle) {
		t.Errorf("err = %v, want ErrAliasCycle", err)
	}
}

func TestLoadAliasShadowsPrimitive(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.string]
description = "Forbidden."

[plans.types.string.fields.body]
type = "string"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected shadow-primitive error")
	}
	if !errors.Is(err, ErrAliasShadowsPrimitive) {
		t.Errorf("err = %v, want ErrAliasShadowsPrimitive", err)
	}
}

func TestLoadAliasUnknownReference(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.start_criteria]
type = "array"
element_type = "Ghost"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected unknown-element-type error for ghost alias")
	}
	if !errors.Is(err, ErrUnknownElementType) {
		t.Errorf("err = %v, want ErrUnknownElementType", err)
	}
}

func TestLoadAliasReferencedFromAlias(t *testing.T) {
	// A has a field whose element_type is B; both expand cleanly.
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.B]
description = "leaf alias"

[plans.types.B.fields.label]
type = "string"
required = true

[plans.types.A]
description = "alias referencing another alias"

[plans.types.A.fields.children]
type = "array"
element_type = "B"

[plans.task]
description = "x"

[plans.task.fields.tree]
type = "array"
element_type = "A"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tree := reg.DBs["plans"].Types["task"].Fields["tree"]
	if tree.ElementType != TypeTable {
		t.Errorf("tree.ElementType = %q, want table", tree.ElementType)
	}
	children, ok := tree.ElementFields["children"]
	if !ok {
		t.Fatal("tree missing children sub-field after alias expansion")
	}
	if children.Type != TypeArray || children.ElementType != TypeTable {
		t.Errorf("children: type=%q elem=%q (want array/table)",
			children.Type, children.ElementType)
	}
	if _, ok := children.ElementFields["label"]; !ok {
		t.Errorf("children missing label sub-field; got %v", children.ElementFields)
	}
}

func TestLoadAliasDuplicateNameAcrossDBs(t *testing.T) {
	// Aliases share a Registry-wide namespace; declaring the same
	// name in two dbs is a load-time error.
	src := `
[a]
paths = ["a.toml"]

[a.types.Shared]
description = "x"

[a.types.Shared.fields.id]
type = "string"
required = true

[a.thing]
description = "x"

[a.thing.fields.id]
type = "string"
required = true

[b]
paths = ["b.toml"]

[b.types.Shared]
description = "x"

[b.types.Shared.fields.id]
type = "string"
required = true

[b.thing]
description = "x"

[b.thing.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected duplicate-alias error")
	}
	if !strings.Contains(err.Error(), "already declared") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

func TestLoadOverlappingPaths(t *testing.T) {
	src := `
[a]
paths = ["shared.toml"]

[a.thing]
description = "x"

[a.thing.fields.id]
type = "string"
required = true

[b]
paths = ["shared.toml"]

[b.thing]
description = "x"

[b.thing.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected overlapping paths error")
	}
	if !errors.Is(err, ErrOverlappingPaths) {
		t.Errorf("err = %v, want ErrOverlappingPaths", err)
	}
}
