package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func fixtureRegistry(t *testing.T) Registry {
	t.Helper()
	reg, err := Load(strings.NewReader(exampleConfig))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return reg
}

func TestValidateOK(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_001", map[string]any{
		"id":     "TASK-001",
		"status": "todo",
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateUnknownDB(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("note.standup.n1", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnknownSectionType) {
		t.Errorf("errors.Is ErrUnknownSectionType = false, err = %v", err)
	}
	if !strings.Contains(err.Error(), "db") {
		t.Errorf("error should name the missing db: %v", err)
	}
}

func TestValidateUnknownType(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.ghost.id", map[string]any{})
	if !errors.Is(err, ErrUnknownSectionType) {
		t.Fatalf("errors.Is ErrUnknownSectionType = false, err = %v", err)
	}
	if !strings.Contains(err.Error(), "type") {
		t.Errorf("error should name the missing type: %v", err)
	}
}

func TestValidateEmptyPath(t *testing.T) {
	reg := fixtureRegistry(t)
	if err := reg.Validate("", nil); !errors.Is(err, ErrUnknownSectionType) {
		t.Fatalf("empty path should return ErrUnknownSectionType, got %v", err)
	}
}

func TestValidateMissingTypeSegment(t *testing.T) {
	reg := fixtureRegistry(t)
	if err := reg.Validate("plans", nil); !errors.Is(err, ErrUnknownSectionType) {
		t.Fatalf("bare db should return ErrUnknownSectionType, got %v", err)
	}
}

func TestValidateMissingRequired(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"id": "TASK-042",
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As ValidationError = false, err = %v", err)
	}
	if len(ve.Failures) != 1 {
		t.Fatalf("failures = %d, want 1", len(ve.Failures))
	}
	f := ve.Failures[0]
	if f.Field != "status" {
		t.Errorf("field = %q", f.Field)
	}
	if f.Kind != FailureMissingRequired {
		t.Errorf("kind = %q", f.Kind)
	}
	if len(f.AllowedValues) != 4 {
		t.Errorf("allowed values = %d, want 4", len(f.AllowedValues))
	}
}

func TestValidateTypeMismatch(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"id":             "TASK-042",
		"status":         "todo",
		"estimate_hours": "three",
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As = false, err = %v", err)
	}
	if len(ve.Failures) != 1 {
		t.Fatalf("failures = %d, want 1", len(ve.Failures))
	}
	f := ve.Failures[0]
	if f.Field != "estimate_hours" || f.Kind != FailureTypeMismatch {
		t.Errorf("got field=%q kind=%q", f.Field, f.Kind)
	}
	if f.ExpectedType != TypeInteger {
		t.Errorf("expected type = %q", f.ExpectedType)
	}
	if f.ActualType != "string" {
		t.Errorf("actual type = %q, want string", f.ActualType)
	}
}

func TestValidateEnumMismatch(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"id":     "TASK-042",
		"status": "cancelled",
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As = false, err = %v", err)
	}
	if len(ve.Failures) != 1 || ve.Failures[0].Kind != FailureEnumMismatch {
		t.Fatalf("failures = %+v", ve.Failures)
	}
}

func TestValidateUnknownField(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"id":      "TASK-042",
		"status":  "todo",
		"mystery": 1,
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As = false, err = %v", err)
	}
	if len(ve.Failures) != 1 || ve.Failures[0].Kind != FailureUnknownField {
		t.Fatalf("failures = %+v", ve.Failures)
	}
}

func TestValidateMultipleFailuresOrdered(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"estimate_hours": "three",
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As = false, err = %v", err)
	}
	if len(ve.Failures) != 3 {
		t.Fatalf("failures = %d, want 3 (id, status missing + estimate_hours type)", len(ve.Failures))
	}
	wantFields := []string{"estimate_hours", "id", "status"}
	for i, want := range wantFields {
		if ve.Failures[i].Field != want {
			t.Errorf("failure[%d] field = %q, want %q", i, ve.Failures[i].Field, want)
		}
	}
}

func TestValidationErrorMessage(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"id":             "TASK-042",
		"status":         "todo",
		"estimate_hours": "three",
	})
	msg := err.Error()
	if !strings.Contains(msg, "validation failed for [plans.task.task_042]:") {
		t.Errorf("missing section prefix: %q", msg)
	}
	if !strings.Contains(msg, `field "estimate_hours" has type "string", expected "integer"`) {
		t.Errorf("missing type-mismatch body: %q", msg)
	}
	if !strings.Contains(msg, "description: Rough hour estimate") {
		t.Errorf("missing description: %q", msg)
	}
}

func TestValidationErrorJSON(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"id": "TASK-042",
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As = false")
	}
	data, mErr := json.Marshal(ve)
	if mErr != nil {
		t.Fatalf("marshal: %v", mErr)
	}
	var round struct {
		SectionPath string `json:"section_path"`
		Failures    []struct {
			Field         string   `json:"field"`
			Kind          string   `json:"kind"`
			Message       string   `json:"message"`
			Description   string   `json:"description"`
			AllowedValues []string `json:"allowed_values"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.SectionPath != "plans.task.task_042" {
		t.Errorf("section_path = %q", round.SectionPath)
	}
	if len(round.Failures) != 1 || round.Failures[0].Field != "status" {
		t.Errorf("failures = %+v", round.Failures)
	}
	if round.Failures[0].Kind != "missing_required" {
		t.Errorf("kind = %q", round.Failures[0].Kind)
	}
}

func TestValidationErrorUnwrap(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As ValidationError = false")
	}
	unwrapped := errors.Unwrap(ve)
	if unwrapped != nil {
		t.Errorf("single Unwrap should be nil for multi-error shape, got %v", unwrapped)
	}
	var ff *FieldFailure
	if !errors.As(err, &ff) {
		t.Fatalf("errors.As FieldFailure = false")
	}
}

func TestValidateIntegerAcceptsFloatWhole(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"id":             "TASK-042",
		"status":         "todo",
		"estimate_hours": float64(3),
	})
	if err != nil {
		t.Fatalf("whole-number float should satisfy integer, got %v", err)
	}
}

func TestValidateIntegerRejectsFraction(t *testing.T) {
	reg := fixtureRegistry(t)
	err := reg.Validate("plans.task.task_042", map[string]any{
		"id":             "TASK-042",
		"status":         "todo",
		"estimate_hours": 3.5,
	})
	if err == nil {
		t.Fatal("fractional float must not satisfy integer")
	}
}

// typeMatrixSchema declares one field per supported TOML type; used to
// exercise valueMatchesType end-to-end.
const typeMatrixSchema = `
[rows]
paths = ["rows.toml"]

[rows.row]
description = "All supported types in one row."

[rows.row.fields.s]
type = "string"
required = true
[rows.row.fields.i]
type = "integer"
required = true
[rows.row.fields.f]
type = "float"
required = true
[rows.row.fields.b]
type = "boolean"
required = true
[rows.row.fields.d]
type = "datetime"
required = true
[rows.row.fields.arr]
type = "array"
required = true
[rows.row.fields.tbl]
type = "table"
required = true
`

func TestValidateTypeMatrix(t *testing.T) {
	reg, err := Load(strings.NewReader(typeMatrixSchema))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	err = reg.Validate("rows.row.x", map[string]any{
		"s":   "hi",
		"i":   42,
		"f":   1.5,
		"b":   true,
		"d":   time.Now(),
		"arr": []any{1, 2, 3},
		"tbl": map[string]any{"k": "v"},
	})
	if err != nil {
		t.Fatalf("type matrix: %v", err)
	}
}

// ----------------------------------------------------------------------------
// F21 — typed array elements + element_fields + alias inlining
// ----------------------------------------------------------------------------

const f21PrimitiveArraySchema = `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "Task with a typed array."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.paths]
type = "array"
element_type = "string"
`

func TestValidateElementTypeMismatch(t *testing.T) {
	reg, err := Load(strings.NewReader(f21PrimitiveArraySchema))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = reg.Validate("plans.task.x", map[string]any{
		"id":    "TASK-001",
		"paths": []any{"a", 2, "c"},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(ve.Failures) != 1 {
		t.Fatalf("failures = %d, want 1", len(ve.Failures))
	}
	f := ve.Failures[0]
	if f.Field != "paths[1]" {
		t.Errorf("field = %q, want paths[1]", f.Field)
	}
	if f.Kind != FailureTypeMismatch {
		t.Errorf("kind = %q, want type_mismatch", f.Kind)
	}
	if f.ExpectedType != TypeString {
		t.Errorf("expected_type = %q, want string", f.ExpectedType)
	}
}

func TestValidateElementTypePassthrough(t *testing.T) {
	reg, err := Load(strings.NewReader(f21PrimitiveArraySchema))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = reg.Validate("plans.task.x", map[string]any{
		"id":    "TASK-001",
		"paths": []any{"a", "b", "c"},
	})
	if err != nil {
		t.Errorf("homogeneous string array should validate, got %v", err)
	}
}

const f21ElementFieldsSchema = `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "Task with an array of typed tables."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.completion_checklist]
type = "array"
element_type = "table"

[plans.task.fields.completion_checklist.element_fields.id]
type = "string"
required = true

[plans.task.fields.completion_checklist.element_fields.text]
type = "string"
required = true

[plans.task.fields.completion_checklist.element_fields.complete]
type = "boolean"
required = true
`

func TestValidateElementFieldsMissingRequired(t *testing.T) {
	reg, err := Load(strings.NewReader(f21ElementFieldsSchema))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = reg.Validate("plans.task.x", map[string]any{
		"id": "TASK-001",
		"completion_checklist": []any{
			map[string]any{"id": "ok", "text": "first", "complete": true},
			map[string]any{"id": "missing-complete", "text": "second"},
		},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(ve.Failures) != 1 {
		t.Fatalf("failures = %d, want 1: %+v", len(ve.Failures), ve.Failures)
	}
	f := ve.Failures[0]
	if f.Field != "completion_checklist[1].complete" {
		t.Errorf("field = %q, want completion_checklist[1].complete", f.Field)
	}
	if f.Kind != FailureMissingRequired {
		t.Errorf("kind = %q, want missing_required", f.Kind)
	}
}

func TestValidateElementFieldsTypeMismatch(t *testing.T) {
	reg, err := Load(strings.NewReader(f21ElementFieldsSchema))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = reg.Validate("plans.task.x", map[string]any{
		"id": "TASK-001",
		"completion_checklist": []any{
			map[string]any{"id": "a", "text": "first", "complete": true},
			map[string]any{"id": "b", "text": "second", "complete": "yes"},
		},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(ve.Failures) != 1 {
		t.Fatalf("failures = %d, want 1: %+v", len(ve.Failures), ve.Failures)
	}
	f := ve.Failures[0]
	if f.Field != "completion_checklist[1].complete" {
		t.Errorf("field = %q, want completion_checklist[1].complete", f.Field)
	}
	if f.Kind != FailureTypeMismatch {
		t.Errorf("kind = %q, want type_mismatch", f.Kind)
	}
	if f.ExpectedType != TypeBoolean || f.ActualType != "string" {
		t.Errorf("types: expected=%q actual=%q", f.ExpectedType, f.ActualType)
	}
}

func TestValidateElementFieldsUnknownField(t *testing.T) {
	reg, err := Load(strings.NewReader(f21ElementFieldsSchema))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = reg.Validate("plans.task.x", map[string]any{
		"id": "TASK-001",
		"completion_checklist": []any{
			map[string]any{"id": "a", "text": "first", "complete": true, "mystery": 7},
		},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(ve.Failures) != 1 {
		t.Fatalf("failures = %d, want 1: %+v", len(ve.Failures), ve.Failures)
	}
	f := ve.Failures[0]
	if f.Field != "completion_checklist[0].mystery" {
		t.Errorf("field = %q, want completion_checklist[0].mystery", f.Field)
	}
	if f.Kind != FailureUnknownField {
		t.Errorf("kind = %q, want unknown_field", f.Kind)
	}
}

func TestValidateNestedElementFields(t *testing.T) {
	// matrix is array of tables; each table has a cells array of strings.
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

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
	err = reg.Validate("plans.task.x", map[string]any{
		"id": "TASK-001",
		"matrix": []any{
			map[string]any{"row_id": "r0", "cells": []any{"a", "b"}},
			map[string]any{"row_id": "r1", "cells": []any{"c", "d"}},
			map[string]any{"row_id": "r2", "cells": []any{"e", 99, "g", "h"}},
		},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(ve.Failures) != 1 {
		t.Fatalf("failures = %d, want 1: %+v", len(ve.Failures), ve.Failures)
	}
	if got := ve.Failures[0].Field; got != "matrix[2].cells[1]" {
		t.Errorf("field = %q, want matrix[2].cells[1]", got)
	}
}

func TestValidateEmptyArrayPasses(t *testing.T) {
	reg, err := Load(strings.NewReader(f21ElementFieldsSchema))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = reg.Validate("plans.task.x", map[string]any{
		"id":                   "TASK-001",
		"completion_checklist": []any{},
	})
	if err != nil {
		t.Errorf("empty array should validate, got %v", err)
	}
}

func TestValidateAliasResolution(t *testing.T) {
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

[plans.types.ChecklistItem.fields.complete]
type = "boolean"
required = true

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.start_criteria]
type = "array"
element_type = "ChecklistItem"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	good := reg.Validate("plans.task.x", map[string]any{
		"id": "TASK-001",
		"start_criteria": []any{
			map[string]any{"id": "1", "text": "first", "complete": true},
			map[string]any{"id": "2", "text": "second", "complete": false},
		},
	})
	if good != nil {
		t.Fatalf("alias-backed validation should pass, got %v", good)
	}

	bad := reg.Validate("plans.task.x", map[string]any{
		"id": "TASK-001",
		"start_criteria": []any{
			map[string]any{"id": "1", "text": "first"}, // missing complete
		},
	})
	var ve *ValidationError
	if !errors.As(bad, &ve) {
		t.Fatalf("expected ValidationError, got %v", bad)
	}
	if len(ve.Failures) != 1 {
		t.Fatalf("failures = %d, want 1: %+v", len(ve.Failures), ve.Failures)
	}
	if got := ve.Failures[0].Field; got != "start_criteria[0].complete" {
		t.Errorf("field = %q, want start_criteria[0].complete", got)
	}
}

func TestValidationErrorJSONForBracketedPath(t *testing.T) {
	reg, err := Load(strings.NewReader(f21ElementFieldsSchema))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = reg.Validate("plans.task.x", map[string]any{
		"id": "TASK-001",
		"completion_checklist": []any{
			map[string]any{"id": "a", "text": "first"}, // missing complete
		},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	data, mErr := json.Marshal(ve)
	if mErr != nil {
		t.Fatalf("marshal: %v", mErr)
	}
	if !strings.Contains(string(data), `"field":"completion_checklist[0].complete"`) {
		t.Errorf("marshalled JSON missing bracketed path; got %s", data)
	}
}

func TestValidateDatetimeFromString(t *testing.T) {
	reg, err := Load(strings.NewReader(`
[rows]
paths = ["rows.toml"]

[rows.row]
description = "Datetime rows."

[rows.row.fields.d]
type = "datetime"
required = true
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cases := []struct {
		name    string
		val     string
		wantErr bool
	}{
		{"rfc3339", "2026-04-20T10:15:00Z", false},
		{"date only", "2026-04-20", false},
		{"not a date", "yesterday", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reg.Validate("rows.row.x", map[string]any{"d": tc.val})
			if (got != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", got, tc.wantErr)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// F22 — schema inheritance via `extends`
// ----------------------------------------------------------------------------

// TestValidateAfterExtendsRequiredFromBase confirms a required field
// inherited from a base behaves identically to a field declared
// directly on the type. The Registry only ever sees the flattened
// SectionType.Fields, so the required check is unaware of provenance.
func TestValidateAfterExtendsRequiredFromBase(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.NodeBase]

[plans.bases.NodeBase.fields.title]
type = "string"
required = true

[plans.task]
description = "x"
extends = "NodeBase"

[plans.task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = reg.Validate("plans.task.t1", map[string]any{
		"id": "t1",
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	var titleFailure *FieldFailure
	for _, f := range ve.Failures {
		if f.Field == "title" {
			titleFailure = f
			break
		}
	}
	if titleFailure == nil {
		t.Fatalf("expected missing-required failure on inherited title; got %+v", ve.Failures)
	}
	if titleFailure.Kind != FailureMissingRequired {
		t.Errorf("kind = %q, want missing_required", titleFailure.Kind)
	}
}

// TestValidateAfterExtendsOverriddenEnum confirms the child's narrowed
// enum is enforced on the resolved type. A value present in the base's
// wider enum but absent from the child's narrowed enum must fail.
func TestValidateAfterExtendsOverriddenEnum(t *testing.T) {
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
	// "done" is in the base enum but NOT in the child's narrowed
	// enum; child wholesale-replace means the value is rejected.
	err = reg.Validate("plans.task.t1", map[string]any{
		"id":     "t1",
		"status": "done",
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(ve.Failures) != 1 || ve.Failures[0].Kind != FailureEnumMismatch {
		t.Fatalf("failures = %+v", ve.Failures)
	}
}
