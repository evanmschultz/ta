package schema

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FailureKind names the category of a single schema validation failure.
// The string form is stable and travels verbatim in the JSON contract emitted
// by *ValidationError, so agent clients can branch on kind without parsing
// free-form messages.
type FailureKind string

// Failure kinds returned by Validate. Each value is emitted verbatim in the
// "kind" field of the per-failure JSON payload.
const (
	// FailureMissingRequired indicates a required field was absent from the
	// supplied section data.
	FailureMissingRequired FailureKind = "missing_required"
	// FailureTypeMismatch indicates a field was present but its Go value did
	// not match the declared schema type.
	FailureTypeMismatch FailureKind = "type_mismatch"
	// FailureEnumMismatch indicates a field's value is not contained in the
	// field's declared enum.
	FailureEnumMismatch FailureKind = "enum_mismatch"
	// FailureUnknownField indicates the supplied data contains a key that is
	// not declared on the target SectionType.
	FailureUnknownField FailureKind = "unknown_field"
)

// FieldFailure is a single field-level validation failure. It implements
// error so it can be returned through errors.Join/Unwrap chains, and it is
// the leaf shape inside ValidationError's JSON payload. Omitted JSON fields
// follow standard omitempty rules — Description, ExpectedType, ActualType,
// and AllowedValues are only populated when relevant to the specific Kind.
type FieldFailure struct {
	// Field is the name of the offending field, as it appeared (or would
	// have appeared) in the section data map.
	Field string `json:"field"`
	// Kind categorizes the failure; see the FailureKind constants.
	Kind FailureKind `json:"kind"`
	// Message is the human-readable description of the failure.
	Message string `json:"message"`
	// Description echoes the schema field's description so agents can surface
	// the intended semantics without a second lookup.
	Description string `json:"description,omitempty"`
	// ExpectedType is the declared schema type, populated for type-mismatch,
	// missing-required, and enum-mismatch failures.
	ExpectedType Type `json:"expected_type,omitempty"`
	// ActualType is the Go type (%T) of the supplied value, populated when a
	// value was present but did not match the schema.
	ActualType string `json:"actual_type,omitempty"`
	// AllowedValues is the field's enum, populated for enum and
	// missing-required failures on enum-constrained fields.
	AllowedValues []any `json:"allowed_values,omitempty"`
}

// Error returns the human-readable failure message.
func (f *FieldFailure) Error() string { return f.Message }

// ValidationError aggregates every field-level failure discovered for one
// section. It implements error, multi-error Unwrap, and json.Marshaler so MCP
// tool handlers can surface failures as structured JSON.
//
// JSON contract (stable; agent-facing):
//
//	{
//	  "section_path": "task.task_001",
//	  "failures": [
//	    {
//	      "field":           "status",
//	      "kind":            "enum_mismatch",
//	      "message":         "field \"status\" value wip is not in allowed set",
//	      "description":     "Task lifecycle state.",
//	      "expected_type":   "string",
//	      "actual_type":     "string",
//	      "allowed_values":  ["todo", "doing", "done"]
//	    }
//	  ]
//	}
//
// Failures are ordered by (Field, Kind) so repeated validations on equivalent
// inputs produce identical payloads.
type ValidationError struct {
	// SectionPath is the full bracketed path of the section that failed
	// validation, e.g. "task.task_001".
	SectionPath string `json:"section_path"`
	// Failures lists every per-field failure detected during validation.
	Failures []*FieldFailure `json:"failures"`
}

// Error renders the validation error in the shape shown in ta.md §Validation.
func (e *ValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "upsert failed for [%s]:", e.SectionPath)
	for _, f := range e.Failures {
		b.WriteString("\n  - ")
		b.WriteString(f.Message)
		if f.Description != "" {
			fmt.Fprintf(&b, "\n    description: %s", f.Description)
		}
		if len(f.AllowedValues) > 0 {
			fmt.Fprintf(&b, "\n    allowed values: %s", formatAllowed(f.AllowedValues))
		}
	}
	return b.String()
}

// Unwrap returns the underlying per-field failures so callers can inspect
// them individually with errors.Is / errors.As.
func (e *ValidationError) Unwrap() []error {
	errs := make([]error, len(e.Failures))
	for i, f := range e.Failures {
		errs[i] = f
	}
	return errs
}

// MarshalJSON emits the ValidationError as JSON suitable for embedding in an
// MCP tool-result error payload.
func (e *ValidationError) MarshalJSON() ([]byte, error) {
	type alias ValidationError
	return json.Marshal((*alias)(e))
}

func formatAllowed(values []any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		if s, ok := v.(string); ok {
			parts[i] = fmt.Sprintf("%q", s)
			continue
		}
		parts[i] = fmt.Sprintf("%v", v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
