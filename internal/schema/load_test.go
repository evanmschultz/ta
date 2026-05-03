package schema

import (
	"errors"
	"reflect"
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

// ----------------------------------------------------------------------------
// F22 — schema inheritance via `extends` keyword
// ----------------------------------------------------------------------------

func TestExtendsHappyPathSingleLevel(t *testing.T) {
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

[plans.task]
description = "x"
extends = "NodeBase"

[plans.task.fields.status]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task := reg.DBs["plans"].Types["task"]
	for _, want := range []string{"parent_id", "title", "status"} {
		if _, ok := task.Fields[want]; !ok {
			t.Errorf("missing field %q after extends; got %v", want, task.Fields)
		}
	}
	if !task.Fields["title"].Required {
		t.Error("inherited title should keep required=true")
	}
}

func TestExtendsHappyPathMultiLevel(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.NodeBase]

[plans.bases.NodeBase.fields.parent_id]
type = "string"

[plans.bases.ActionItem]
extends = "NodeBase"

[plans.bases.ActionItem.fields.role]
type = "string"
required = true

[plans.bases.QAItem]
extends = "ActionItem"

[plans.bases.QAItem.fields.outcome]
type = "string"

[plans.task]
description = "x"
extends = "QAItem"

[plans.task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task := reg.DBs["plans"].Types["task"]
	for _, want := range []string{"parent_id", "role", "outcome", "id"} {
		if _, ok := task.Fields[want]; !ok {
			t.Errorf("missing field %q after multi-level extends; got %v", want, task.Fields)
		}
	}
}

func TestExtendsOverrideField(t *testing.T) {
	// Base declares `status` enum {todo, doing, done}; child narrows
	// to {todo, doing}. Wholesale replace — child enum wins.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.WithStatus]

[plans.bases.WithStatus.fields.status]
type = "string"
enum = ["todo", "doing", "done"]
required = true

[plans.task]
description = "x"
extends = "WithStatus"

[plans.task.fields.status]
type = "string"
enum = ["todo", "doing"]
required = true

[plans.task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	status := reg.DBs["plans"].Types["task"].Fields["status"]
	if got := len(status.Enum); got != 2 {
		t.Errorf("status.Enum length = %d, want 2 (child wholesale-replaces)", got)
	}
}

func TestExtendsOverridesAreWholesale(t *testing.T) {
	// Base sets required=true and format=markdown; child redeclares
	// with only type — required must drop to false, format must drop.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.WithBody]

[plans.bases.WithBody.fields.body]
type = "string"
required = true
format = "markdown"

[plans.task]
description = "x"
extends = "WithBody"

[plans.task.fields.body]
type = "string"

[plans.task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	body := reg.DBs["plans"].Types["task"].Fields["body"]
	if body.Required {
		t.Error("body.Required should drop to false on wholesale replace")
	}
	if body.Format != "" {
		t.Errorf("body.Format should drop to empty, got %q", body.Format)
	}
}

func TestExtendsDeepCloneIndependence(t *testing.T) {
	// Mutating a flattened child field's Enum slice must NOT alter the
	// state visible through any other inheriting type. Two children
	// inheriting the same base each get their own copy.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.WithFlags]

[plans.bases.WithFlags.fields.tags]
type = "array"
element_type = "string"

[plans.task]
description = "x"
extends = "WithFlags"

[plans.task.fields.id]
type = "string"
required = true

[plans.note]
description = "x"
extends = "WithFlags"

[plans.note.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	taskTags := reg.DBs["plans"].Types["task"].Fields["tags"]
	noteTags := reg.DBs["plans"].Types["note"].Fields["tags"]
	if &taskTags == &noteTags {
		t.Fatal("task and note tags share the same struct — clone broke")
	}
	// A defensive check: mutate task ElementFields (would-be aliasing
	// surface) and confirm note unaffected. tags has no ElementFields
	// here, so set a sentinel and verify isolation.
	taskField := reg.DBs["plans"].Types["task"].Fields["tags"]
	taskField.ElementFields = map[string]Field{"sentinel": {Name: "sentinel"}}
	reg.DBs["plans"].Types["task"].Fields["tags"] = taskField
	noteAfter := reg.DBs["plans"].Types["note"].Fields["tags"]
	if _, leaked := noteAfter.ElementFields["sentinel"]; leaked {
		t.Error("mutation on task tags leaked into note tags — base fields aren't deep-cloned per inheritor")
	}
}

func TestExtendsCycleDetectionSelfReference(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.A]
extends = "A"

[plans.bases.A.fields.x]
type = "string"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected ErrExtendsCycle for self-referential base")
	}
	if !errors.Is(err, ErrExtendsCycle) {
		t.Errorf("err = %v, want ErrExtendsCycle", err)
	}
	if !strings.Contains(err.Error(), "A → A") {
		t.Errorf("error should show cycle path A → A: %v", err)
	}
}

func TestExtendsCycleDetectionMutual(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.A]
extends = "B"

[plans.bases.A.fields.x]
type = "string"

[plans.bases.B]
extends = "A"

[plans.bases.B.fields.y]
type = "string"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected ErrExtendsCycle for mutual base chain")
	}
	if !errors.Is(err, ErrExtendsCycle) {
		t.Errorf("err = %v, want ErrExtendsCycle", err)
	}
}

func TestExtendsCycleDetectionLong(t *testing.T) {
	// A → B → C → D → A
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.A]
extends = "B"

[plans.bases.A.fields.a]
type = "string"

[plans.bases.B]
extends = "C"

[plans.bases.B.fields.b]
type = "string"

[plans.bases.C]
extends = "D"

[plans.bases.C.fields.c]
type = "string"

[plans.bases.D]
extends = "A"

[plans.bases.D.fields.d]
type = "string"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected ErrExtendsCycle for 4-step chain")
	}
	if !errors.Is(err, ErrExtendsCycle) {
		t.Errorf("err = %v, want ErrExtendsCycle", err)
	}
	// The chain message must capture every node visited; deterministic
	// order is per-walk, but each name appears at least once.
	for _, name := range []string{"A", "B", "C", "D"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("cycle message missing node %q: %v", name, err)
		}
	}
}

func TestExtendsUnknownBase(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"
extends = "Bogus"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected ErrUnknownBase for nonexistent base")
	}
	if !errors.Is(err, ErrUnknownBase) {
		t.Errorf("err = %v, want ErrUnknownBase", err)
	}
}

func TestExtendsEmptyBaseRejected(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.Empty]
description = "no fields, no extends"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected ErrEmptyBase for empty base body")
	}
	if !errors.Is(err, ErrEmptyBase) {
		t.Errorf("err = %v, want ErrEmptyBase", err)
	}
}

func TestExtendsBasesAcrossDBs(t *testing.T) {
	// db1 declares the base; db2's type extends it. Bases share a
	// Registry-wide namespace so the cross-db reference works.
	src := `
[a]
paths = ["a.toml"]

[a.bases.Shared]

[a.bases.Shared.fields.shared_id]
type = "string"
required = true

[a.thing]
description = "x"

[a.thing.fields.id]
type = "string"
required = true

[b]
paths = ["b.toml"]

[b.thing]
description = "x"
extends = "Shared"

[b.thing.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bThing := reg.DBs["b"].Types["thing"]
	if _, ok := bThing.Fields["shared_id"]; !ok {
		t.Errorf("b.thing missing inherited shared_id field; got %v", bThing.Fields)
	}
}

func TestExtendsDuplicateBaseName(t *testing.T) {
	// Bases share a Registry-wide namespace; declaring the same name
	// in two dbs is a load-time error.
	src := `
[a]
paths = ["a.toml"]

[a.bases.Dupe]

[a.bases.Dupe.fields.id]
type = "string"

[a.thing]
description = "x"

[a.thing.fields.id]
type = "string"
required = true

[b]
paths = ["b.toml"]

[b.bases.Dupe]

[b.bases.Dupe.fields.id]
type = "string"

[b.thing]
description = "x"

[b.thing.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected duplicate-base error")
	}
	if !strings.Contains(err.Error(), "already declared") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

func TestExtendsLoadDeterminism(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.NodeBase]

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
	reg1, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load 1: %v", err)
	}
	reg2, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if !reflect.DeepEqual(reg1, reg2) {
		t.Errorf("repeated loads differ — flatten is not deterministic")
	}
}

func TestExtendsBaseWithArrayFieldElementTypeAlias(t *testing.T) {
	// Base declares a field whose element_type names an alias. Type
	// extends that base. Phase ordering (B.0 before B) means the
	// alias inlines correctly into the inherited field.
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.ChecklistItem]
description = "Reusable shape."

[plans.types.ChecklistItem.fields.id]
type = "string"
required = true

[plans.types.ChecklistItem.fields.text]
type = "string"
required = true

[plans.bases.WithChecklist]

[plans.bases.WithChecklist.fields.items]
type = "array"
element_type = "ChecklistItem"

[plans.task]
description = "x"
extends = "WithChecklist"

[plans.task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	items := reg.DBs["plans"].Types["task"].Fields["items"]
	if items.ElementType != TypeTable {
		t.Errorf("items.ElementType = %q, want table (alias inlined)", items.ElementType)
	}
	if _, ok := items.ElementFields["id"]; !ok {
		t.Errorf("items.ElementFields missing id from alias; got %v", items.ElementFields)
	}
	if _, ok := items.ElementFields["text"]; !ok {
		t.Errorf("items.ElementFields missing text from alias; got %v", items.ElementFields)
	}
}

func TestExtendsConcreteTypeNotExtensible(t *testing.T) {
	// extends names an existing concrete record type. Bases-only
	// per F22 — surface ErrExtendsTargetNotBase.
	src := `
[plans]
paths = ["plans.toml"]

[plans.parent]
description = "concrete type"

[plans.parent.fields.id]
type = "string"
required = true

[plans.child]
description = "extends a concrete type"
extends = "parent"

[plans.child.fields.note]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected ErrExtendsTargetNotBase")
	}
	if !errors.Is(err, ErrExtendsTargetNotBase) {
		t.Errorf("err = %v, want ErrExtendsTargetNotBase", err)
	}
}

func TestExtendsOnFieldRejected(t *testing.T) {
	// extends on a field body is not a recognized field-key; the
	// existing unknown-key path must reject it.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
extends = "Something"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected unknown-field-key error for extends on a field body")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error should be unknown-key: %v", err)
	}
}

func TestExtendsAliasRejectsExtends(t *testing.T) {
	// An alias body cannot use extends; surface
	// ErrAliasExtendsNotAllowed.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.NodeBase]

[plans.bases.NodeBase.fields.id]
type = "string"

[plans.types.MyAlias]
extends = "NodeBase"

[plans.types.MyAlias.fields.label]
type = "string"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected ErrAliasExtendsNotAllowed")
	}
	if !errors.Is(err, ErrAliasExtendsNotAllowed) {
		t.Errorf("err = %v, want ErrAliasExtendsNotAllowed", err)
	}
}

func TestExtendsBaseAliasNameCollision(t *testing.T) {
	// Same name appears in [<db>.bases] and [<db>.types]; reject.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.Dupe]

[plans.bases.Dupe.fields.x]
type = "string"

[plans.types.Dupe]

[plans.types.Dupe.fields.y]
type = "string"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected ErrBaseAliasNameCollision")
	}
	if !errors.Is(err, ErrBaseAliasNameCollision) {
		t.Errorf("err = %v, want ErrBaseAliasNameCollision", err)
	}
}

func TestExtendsTypeWithOnlyExtendsAndDescription(t *testing.T) {
	// A type may declare zero own fields when it extends a base that
	// supplies them. Required-field check on the resolved type.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.NodeBase]

[plans.bases.NodeBase.fields.id]
type = "string"
required = true

[plans.task]
description = "task that's just a NodeBase rename"
extends = "NodeBase"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task := reg.DBs["plans"].Types["task"]
	if _, ok := task.Fields["id"]; !ok {
		t.Errorf("task missing inherited id field; got %v", task.Fields)
	}
	if !task.Fields["id"].Required {
		t.Error("inherited id should be required")
	}
}

func TestExtendsBaseWithoutExtendsAllowed(t *testing.T) {
	// A base without `extends` is the root of a chain — explicitly
	// allowed.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.Root]
description = "Root of an inheritance chain."

[plans.bases.Root.fields.id]
type = "string"
required = true

[plans.task]
description = "x"
extends = "Root"

[plans.task.fields.note]
type = "string"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task := reg.DBs["plans"].Types["task"]
	if _, ok := task.Fields["id"]; !ok {
		t.Errorf("task missing inherited id field; got %v", task.Fields)
	}
	if _, ok := task.Fields["note"]; !ok {
		t.Errorf("task missing own note field; got %v", task.Fields)
	}
}

// TestExtendsBaseNameCollisionMatrix locks the F22 cross-namespace
// collision rule: a declared base name MUST NOT match any other
// declared symbol (alias, base, concrete type) anywhere in the
// Registry. Bases are global symbols so the discipline holds across
// dbs.
//
// The pure base-vs-base case is covered by collectBases (with a
// "duplicate base" message); the pure base-vs-alias case is covered
// by checkBaseAliasCollision (with ErrBaseAliasNameCollision). The
// remaining shapes — base-vs-concrete-type — are surfaced through
// ErrBaseNameCollision by checkBaseNameCollisions.
func TestExtendsBaseNameCollisionMatrix(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr error
	}{
		{
			name: "base name shadows concrete type in same db",
			src: `
[plans]
paths = ["plans.toml"]

[plans.bases.NodeBase]

[plans.bases.NodeBase.fields.x]
type = "string"

[plans.NodeBase]
description = "concrete type sharing the base name"

[plans.NodeBase.fields.id]
type = "string"
required = true
`,
			wantErr: ErrBaseNameCollision,
		},
		{
			name: "base name shadows concrete type in different db",
			src: `
[a]
paths = ["a.toml"]

[a.someType]
description = "concrete in a"

[a.someType.fields.id]
type = "string"
required = true

[b]
paths = ["b.toml"]

[b.bases.someType]

[b.bases.someType.fields.x]
type = "string"

[b.thing]
description = "x"

[b.thing.fields.id]
type = "string"
required = true
`,
			wantErr: ErrBaseNameCollision,
		},
		{
			// Alias-vs-base same db — prior F22 behaviour, must still
			// trip ErrBaseAliasNameCollision (the more specific
			// message), not ErrBaseNameCollision.
			name: "base name collides with alias in same db",
			src: `
[plans]
paths = ["plans.toml"]

[plans.bases.Dupe]

[plans.bases.Dupe.fields.x]
type = "string"

[plans.types.Dupe]

[plans.types.Dupe.fields.y]
type = "string"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
`,
			wantErr: ErrBaseAliasNameCollision,
		},
		{
			// Alias-vs-base different db — Registry-wide alias namespace
			// means the same name in [a.types.X] and [b.bases.X] also
			// trips the alias-vs-base check.
			name: "base name collides with alias in different db",
			src: `
[a]
paths = ["a.toml"]

[a.types.SharedShape]

[a.types.SharedShape.fields.x]
type = "string"

[a.thing]
description = "x"

[a.thing.fields.id]
type = "string"
required = true

[b]
paths = ["b.toml"]

[b.bases.SharedShape]

[b.bases.SharedShape.fields.y]
type = "string"

[b.task]
description = "x"

[b.task.fields.id]
type = "string"
required = true
`,
			wantErr: ErrBaseAliasNameCollision,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(tc.src))
			if err == nil {
				t.Fatalf("expected %v, got nil", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestExtendsDeepCloneEnumIndependence proves the F22 cloneField
// deep-copy of Enum keeps siblings inheriting from the same base
// independent. The pre-fix code aliased the same backing []any across
// all flattened children, so anyone mutating a child's resolved Enum
// (e.g. via ValidationError.Failures[].AllowedValues) would corrupt
// every sibling's view.
func TestExtendsDeepCloneEnumIndependence(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.WithStatus]

[plans.bases.WithStatus.fields.status]
type = "string"
enum = ["todo", "doing", "done"]
required = true

[plans.task]
description = "x"
extends = "WithStatus"

[plans.task.fields.id]
type = "string"
required = true

[plans.note]
description = "x"
extends = "WithStatus"

[plans.note.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	taskStatus := reg.DBs["plans"].Types["task"].Fields["status"]
	noteStatus := reg.DBs["plans"].Types["note"].Fields["status"]
	if len(taskStatus.Enum) != 3 || len(noteStatus.Enum) != 3 {
		t.Fatalf("expected length-3 enums on both siblings; task=%v note=%v",
			taskStatus.Enum, noteStatus.Enum)
	}
	// Mutate task's resolved Enum. If the slice is aliased back to
	// the cached base copy, note's Enum will see the same change.
	taskStatus.Enum[0] = "MUTATED"
	if noteStatus.Enum[0] == "MUTATED" {
		t.Errorf(
			"note.status.Enum aliased task.status.Enum — cloneField did not deep-copy Enum: note=%v",
			noteStatus.Enum)
	}
	// Sanity: the registry's stored copy must also remain stable.
	stored := reg.DBs["plans"].Types["note"].Fields["status"].Enum
	if stored[0] == "MUTATED" {
		t.Errorf(
			"registry-stored note.status.Enum mutated through task — alias leak: stored=%v",
			stored)
	}
}

// ----------------------------------------------------------------------------
// F28 — direct nested-table inner-shape validation (`fields` on tables).
// ----------------------------------------------------------------------------

func TestBuildField_AcceptsFields(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.contract]
type = "table"
description = "Structured completion gate."

[plans.task.fields.contract.fields.start]
type = "string"

[plans.task.fields.contract.fields.complete]
type = "boolean"
default = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	contract := reg.DBs["plans"].Types["task"].Fields["contract"]
	if contract.Type != TypeTable {
		t.Fatalf("contract.Type = %q, want %q", contract.Type, TypeTable)
	}
	start, ok := contract.Fields["start"]
	if !ok {
		t.Fatal("contract.Fields missing start")
	}
	if start.Type != TypeString {
		t.Errorf("contract.Fields[start].Type = %q, want %q", start.Type, TypeString)
	}
	complete, ok := contract.Fields["complete"]
	if !ok {
		t.Fatal("contract.Fields missing complete")
	}
	if complete.Type != TypeBoolean {
		t.Errorf("contract.Fields[complete].Type = %q, want %q", complete.Type, TypeBoolean)
	}
}

func TestBuildField_RejectsFieldsOnNonTable(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.bogus]
type = "string"

[plans.task.fields.bogus.fields.inner]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for fields on non-table field")
	}
	if !strings.Contains(err.Error(), `fields is only valid on type = "table"`) {
		t.Errorf("error should mention the type-table requirement: %v", err)
	}
}

func TestBuildField_RejectsFieldsOnArray(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.things]
type = "array"
element_type = "string"

[plans.task.fields.things.fields.inner]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for fields on array field")
	}
	if !strings.Contains(err.Error(), `fields is only valid on type = "table"`) {
		t.Errorf("error should mention the type-table requirement: %v", err)
	}
}

func TestBuildField_RejectsFieldsAlongsideElementFields(t *testing.T) {
	// Regression: `type = "table"` with stray `element_fields` was already
	// rejected by the F21 invariant (element_fields requires type=array).
	// Make sure introducing F28's `fields` key did not silently relax that
	// gate.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.contract]
type = "table"

[plans.task.fields.contract.element_fields.start]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for element_fields on table-typed field")
	}
	if !strings.Contains(err.Error(), `element_fields is only valid on type = "array"`) {
		t.Errorf("error should mention the array-only requirement: %v", err)
	}
}

func TestBuildField_RejectsFieldsAlongsideElementType(t *testing.T) {
	// Regression: `type = "table"` plus `element_type` was already rejected
	// by F21 (element_type requires type=array). F28 must not relax that.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.contract]
type = "table"
element_type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for element_type on table-typed field")
	}
	if !strings.Contains(err.Error(), `element_type is only valid on type = "array"`) {
		t.Errorf("error should mention the array-only requirement: %v", err)
	}
}

func TestBuildField_NestedFieldsRecursive(t *testing.T) {
	// Three-level deep nested-table chain: contract.checklist.item.text.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.contract]
type = "table"

[plans.task.fields.contract.fields.checklist]
type = "table"

[plans.task.fields.contract.fields.checklist.fields.item]
type = "table"

[plans.task.fields.contract.fields.checklist.fields.item.fields.text]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	contract := reg.DBs["plans"].Types["task"].Fields["contract"]
	checklist, ok := contract.Fields["checklist"]
	if !ok {
		t.Fatal("missing contract.fields.checklist")
	}
	item, ok := checklist.Fields["item"]
	if !ok {
		t.Fatal("missing contract.fields.checklist.fields.item")
	}
	text, ok := item.Fields["text"]
	if !ok {
		t.Fatal("missing contract.fields.checklist.fields.item.fields.text")
	}
	if text.Type != TypeString {
		t.Errorf("leaf text.Type = %q, want %q", text.Type, TypeString)
	}
	if !text.Required {
		t.Error("leaf text.Required should be true")
	}
}

func TestBuildField_NestedFieldsArrayInside(t *testing.T) {
	// Nested table with an array sub-field: the inner array's element_type
	// must thread through buildField.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.contract]
type = "table"

[plans.task.fields.contract.fields.tags]
type = "array"
element_type = "string"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	contract := reg.DBs["plans"].Types["task"].Fields["contract"]
	tags, ok := contract.Fields["tags"]
	if !ok {
		t.Fatal("missing contract.fields.tags")
	}
	if tags.Type != TypeArray {
		t.Errorf("tags.Type = %q, want %q", tags.Type, TypeArray)
	}
	if tags.ElementType != TypeString {
		t.Errorf("tags.ElementType = %q, want %q", tags.ElementType, TypeString)
	}
}

func TestBuildField_NestedFieldsAliasInside(t *testing.T) {
	// A nested-table sub-field declares element_type = "<alias>"; alias
	// inlining must recurse through Fields to reach it.
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.ChecklistItem]
description = "x"

[plans.types.ChecklistItem.fields.id]
type = "string"
required = true

[plans.types.ChecklistItem.fields.complete]
type = "boolean"

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.contract]
type = "table"

[plans.task.fields.contract.fields.items]
type = "array"
element_type = "ChecklistItem"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	contract := reg.DBs["plans"].Types["task"].Fields["contract"]
	items, ok := contract.Fields["items"]
	if !ok {
		t.Fatal("missing contract.fields.items")
	}
	// After inlining, items.ElementType should resolve to "table" and
	// ElementFields should carry the alias's resolved fields.
	if items.ElementType != TypeTable {
		t.Errorf("items.ElementType = %q, want %q (alias should inline)", items.ElementType, TypeTable)
	}
	if _, ok := items.ElementFields["id"]; !ok {
		t.Errorf("items.ElementFields missing id sub-field; got %v", items.ElementFields)
	}
	if _, ok := items.ElementFields["complete"]; !ok {
		t.Errorf("items.ElementFields missing complete sub-field; got %v", items.ElementFields)
	}
}

func TestBuildField_RejectsUnknownKeyMessageMentionsFields(t *testing.T) {
	// The unknown-key error string must list `fields` so agents can
	// discover the new key.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true
bogus_key = 42
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "fields") {
		t.Errorf("unknown-key error should list `fields` in allowed set: %v", err)
	}
}

func TestCloneField_DeepClonesFields(t *testing.T) {
	// Two siblings extending the same base whose field carries an inner
	// nested-table shape. Mutating one resolved sibling's Fields map
	// must not bleed into the other.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.WithContract]
description = "x"

[plans.bases.WithContract.fields.contract]
type = "table"

[plans.bases.WithContract.fields.contract.fields.start]
type = "string"

[plans.bases.WithContract.fields.contract.fields.complete]
type = "boolean"

[plans.task]
description = "x"
extends = "WithContract"

[plans.task.fields.id]
type = "string"
required = true

[plans.note]
description = "x"
extends = "WithContract"

[plans.note.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	taskContract := reg.DBs["plans"].Types["task"].Fields["contract"]
	noteContract := reg.DBs["plans"].Types["note"].Fields["contract"]
	if len(taskContract.Fields) != 2 || len(noteContract.Fields) != 2 {
		t.Fatalf("expected 2 sub-fields on each sibling; task=%v note=%v",
			taskContract.Fields, noteContract.Fields)
	}
	// Mutate one sibling's resolved Fields map. If the maps alias the
	// shared base copy, the other sibling will see the change too.
	delete(taskContract.Fields, "start")
	if _, ok := noteContract.Fields["start"]; !ok {
		t.Errorf("note.contract.Fields aliased task.contract.Fields — cloneField did not deep-copy Fields")
	}
}

func TestExpandBases_NestedTableInBase(t *testing.T) {
	// A base declares a nested-table field. The concrete type extending
	// it must inherit the inner-shape declaration.
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.WithContract]
description = "x"

[plans.bases.WithContract.fields.contract]
type = "table"

[plans.bases.WithContract.fields.contract.fields.start]
type = "string"
required = true

[plans.task]
description = "x"
extends = "WithContract"

[plans.task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	contract := reg.DBs["plans"].Types["task"].Fields["contract"]
	if contract.Type != TypeTable {
		t.Errorf("inherited contract.Type = %q, want %q", contract.Type, TypeTable)
	}
	start, ok := contract.Fields["start"]
	if !ok {
		t.Fatal("inherited contract missing fields.start")
	}
	if !start.Required {
		t.Error("inherited start.Required should be true")
	}
}
