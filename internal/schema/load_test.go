package schema

import (
	"strings"
	"testing"
)

const exampleConfig = `
[schema.task]
description = "A unit of work an agent picks up"

[schema.task.fields.id]
type = "string"
required = true
description = "Stable identifier, e.g. 'TASK-001'"

[schema.task.fields.status]
type = "string"
required = true
enum = ["todo", "in_progress", "done", "blocked"]
description = "Current state of the task"

[schema.task.fields.body]
type = "string"
required = false
format = "markdown"
description = "Freeform writeup. Markdown with code fences supported."

[schema.task.fields.estimate_hours]
type = "integer"
required = false
description = "Rough hour estimate"
`

func TestLoadHappyPath(t *testing.T) {
	reg, err := Load(strings.NewReader(exampleConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task, ok := reg.Types["task"]
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

func TestLoadRejectsUnsupportedType(t *testing.T) {
	src := `
[schema.note.fields.tags]
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

func TestLoadRejectsUnknownFields(t *testing.T) {
	src := `
[schema.task]
typo_field = "oops"

[schema.task.fields.id]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown top-level key")
	}
}

func TestLoadEmpty(t *testing.T) {
	reg, err := Load(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(reg.Types) != 0 {
		t.Errorf("types = %d, want 0", len(reg.Types))
	}
}

func TestLoadMalformedTOML(t *testing.T) {
	_, err := Load(strings.NewReader("[schema.task"))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error = %q, want wrapping prefix", err)
	}
}
