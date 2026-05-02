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
