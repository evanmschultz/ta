package schema

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FailureKind names the category of a single schema validation failure.
type FailureKind string

// Failure kinds returned by Validate.
const (
	FailureMissingRequired FailureKind = "missing_required"
	FailureTypeMismatch    FailureKind = "type_mismatch"
	FailureEnumMismatch    FailureKind = "enum_mismatch"
	FailureUnknownField    FailureKind = "unknown_field"
)

// FieldFailure is a single field-level validation failure. It implements
// error so it can be returned through errors.Join/Unwrap chains.
type FieldFailure struct {
	Field         string      `json:"field"`
	Kind          FailureKind `json:"kind"`
	Message       string      `json:"message"`
	Description   string      `json:"description,omitempty"`
	ExpectedType  Type        `json:"expected_type,omitempty"`
	ActualType    string      `json:"actual_type,omitempty"`
	AllowedValues []any       `json:"allowed_values,omitempty"`
}

// Error returns the human-readable failure message.
func (f *FieldFailure) Error() string { return f.Message }

// ValidationError aggregates every field-level failure for one section.
// It implements error, multi-error Unwrap, and json.Marshaler so MCP tool
// handlers can surface the failures as structured JSON.
type ValidationError struct {
	SectionPath string          `json:"section_path"`
	Failures    []*FieldFailure `json:"failures"`
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
