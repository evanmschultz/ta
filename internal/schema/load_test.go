package schema

import (
	"strings"
	"testing"
)

// exampleConfig is a single-instance TOML db with four fields, used by
// tests in this package as a reusable fixture.
const exampleConfig = `
[plans]
file = "plans.toml"
format = "toml"
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
description = "Freeform writeup. Markdown with code fences supported."

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
	if db.Shape != ShapeFile {
		t.Errorf("shape = %q, want %q", db.Shape, ShapeFile)
	}
	if db.Path != "plans.toml" {
		t.Errorf("path = %q, want plans.toml", db.Path)
	}
	if db.Format != FormatTOML {
		t.Errorf("format = %q, want toml", db.Format)
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

	status, ok := task.Fields["status"]
	if !ok {
		t.Fatal("missing status field")
	}
	if status.Type != TypeString {
		t.Errorf("status type = %q", status.Type)
	}
	if !status.Required {
		t.Error("status must be required")
	}
	if got := len(status.Enum); got != 4 {
		t.Errorf("status enum len = %d, want 4", got)
	}

	body := task.Fields["body"]
	if body.Format != "markdown" {
		t.Errorf("body format = %q", body.Format)
	}
	if body.Required {
		t.Error("body must not be required")
	}

	est := task.Fields["estimate_hours"]
	if est.Type != TypeInteger {
		t.Errorf("estimate_hours type = %q", est.Type)
	}
}

func TestLoadRejectsUnsupportedFieldType(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
format = "toml"

[plans.note]
description = "x"

[plans.note.fields.tags]
type = "set"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("error = %q, want unsupported-type message", err)
	}
}

func TestLoadRejectsMissingShapeSelector(t *testing.T) {
	src := `
[plans]
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for missing shape selector")
	}
	if !strings.Contains(err.Error(), "shape selector") {
		t.Errorf("error = %q, want shape-selector message", err)
	}
}

func TestLoadRejectsMultipleShapeSelectors(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
directory = "workflow"
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for multiple shape selectors")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want mutually-exclusive message", err)
	}
}

func TestLoadRejectsMissingFormat(t *testing.T) {
	src := `
[plans]
file = "plans.toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for missing format")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error = %q, want format-required message", err)
	}
}

func TestLoadRejectsBadFormat(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
format = "yaml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsFileExtFormatMismatch(t *testing.T) {
	src := `
[plans]
file = "plans.md"
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for ext/format mismatch")
	}
	if !strings.Contains(err.Error(), "extension does not match format") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsTypeWithoutDescription(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
format = "toml"

[plans.task]

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for type without description")
	}
	if !strings.Contains(err.Error(), "description") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsTypeWithoutFields(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
format = "toml"

[plans.task]
description = "A task"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for type without fields")
	}
	if !strings.Contains(err.Error(), "at least one field") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsMDWithoutHeading(t *testing.T) {
	src := `
[readme]
file = "README.md"
format = "md"

[readme.section]
description = "An H2 section."

[readme.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for MD type without heading")
	}
	if !strings.Contains(err.Error(), "heading") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsMDHeadingOutOfRange(t *testing.T) {
	src := `
[readme]
file = "README.md"
format = "md"

[readme.section]
description = "Section"
heading = 7

[readme.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for heading out of range")
	}
	if !strings.Contains(err.Error(), "1..6") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsDuplicateMDHeading(t *testing.T) {
	src := `
[readme]
file = "README.md"
format = "md"

[readme.title]
description = "H1"
heading = 2

[readme.title.fields.body]
type = "string"

[readme.section]
description = "Also H2"
heading = 2

[readme.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for duplicate heading")
	}
	if !strings.Contains(err.Error(), "heading") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsHeadingOnTOMLDB(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
format = "toml"

[plans.task]
description = "A task"
heading = 2

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for heading on TOML db")
	}
	if !strings.Contains(err.Error(), "heading") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsDuplicatePath(t *testing.T) {
	src := `
[a]
file = "same.toml"
format = "toml"

[a.task]
description = "A"

[a.task.fields.id]
type = "string"
required = true

[b]
file = "same.toml"
format = "toml"

[b.task]
description = "B"

[b.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for duplicate path across dbs")
	}
	if !strings.Contains(err.Error(), "same.toml") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsNestedPaths(t *testing.T) {
	src := `
[outer]
directory = "work"
format = "toml"

[outer.task]
description = "Outer"

[outer.task.fields.id]
type = "string"
required = true

[inner]
directory = "work/sub"
format = "toml"

[inner.task]
description = "Inner"

[inner.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for nested paths")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadAcceptsDirectoryShape(t *testing.T) {
	src := `
[plan_db]
directory = "workflow"
format = "toml"
description = "Drops."

[plan_db.build_task]
description = "Work unit."

[plan_db.build_task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db := reg.DBs["plan_db"]
	if db.Shape != ShapeDirectory {
		t.Errorf("shape = %q", db.Shape)
	}
	if db.Path != "workflow" {
		t.Errorf("path = %q", db.Path)
	}
}

func TestLoadAcceptsCollectionShape(t *testing.T) {
	src := `
[docs]
collection = "docs"
format = "md"
description = "Pages."

[docs.section]
description = "An H2 section."
heading = 2

[docs.section.fields.body]
type = "string"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db := reg.DBs["docs"]
	if db.Shape != ShapeCollection {
		t.Errorf("shape = %q", db.Shape)
	}
	if db.Path != "docs" {
		t.Errorf("path = %q", db.Path)
	}
	if db.Format != FormatMD {
		t.Errorf("format = %q", db.Format)
	}
	if got := db.Types["section"].Heading; got != 2 {
		t.Errorf("heading = %d", got)
	}
}

func TestLoadEmpty(t *testing.T) {
	reg, err := Load(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(reg.DBs) != 0 {
		t.Errorf("dbs = %d, want 0", len(reg.DBs))
	}
}

func TestLoadMalformedTOML(t *testing.T) {
	_, err := Load(strings.NewReader("[plans"))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error = %q, want wrapping prefix", err)
	}
}

func TestLoadRejectsUnknownTypeKey(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
format = "toml"

[plans.task]
description = "A task"
wat = "huh"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown type key")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsUnknownFieldKey(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
bogus = 1
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown field key")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadBytes(t *testing.T) {
	reg, err := LoadBytes([]byte(exampleConfig))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if _, ok := reg.DBs["plans"]; !ok {
		t.Fatal("LoadBytes did not produce plans db")
	}
}

func TestLoadRejectsNonTableTopLevel(t *testing.T) {
	src := `plans = "oops"`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for non-table top-level entry")
	}
	if !strings.Contains(err.Error(), "must be a table") {
		t.Errorf("error = %q", err)
	}
}
