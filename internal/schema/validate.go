package schema

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"
)

// ErrUnknownSectionType is returned by Validate when the first segment of
// the section path has no registered schema type.
var ErrUnknownSectionType = errors.New("unknown section type")

// Validate checks data against the schema entry selected by sectionPath.
// It returns nil if the data conforms, ErrUnknownSectionType wrapped with
// the offending type name if no schema is registered, or a *ValidationError
// aggregating every field-level failure.
func (r Registry) Validate(sectionPath string, data map[string]any) error {
	st, ok := r.Lookup(sectionPath)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownSectionType, firstSegment(sectionPath))
	}

	var failures []*FieldFailure

	for _, name := range sortedKeys(st.Fields) {
		field := st.Fields[name]
		if !field.Required {
			continue
		}
		if _, present := data[name]; present {
			continue
		}
		failures = append(failures, &FieldFailure{
			Field:         name,
			Kind:          FailureMissingRequired,
			Message:       fmt.Sprintf("missing required field %q", name),
			Description:   field.Description,
			AllowedValues: field.Enum,
			ExpectedType:  field.Type,
		})
	}

	for _, name := range sortedKeys(data) {
		val := data[name]
		field, ok := st.Fields[name]
		if !ok {
			failures = append(failures, &FieldFailure{
				Field:   name,
				Kind:    FailureUnknownField,
				Message: fmt.Sprintf("unknown field %q for section type %q", name, st.Name),
			})
			continue
		}
		actual := describeType(val)
		if !valueMatchesType(field.Type, val) {
			failures = append(failures, &FieldFailure{
				Field:        name,
				Kind:         FailureTypeMismatch,
				Message:      fmt.Sprintf("field %q has type %q, expected %q", name, actual, field.Type),
				Description:  field.Description,
				ExpectedType: field.Type,
				ActualType:   actual,
			})
			continue
		}
		if len(field.Enum) > 0 && !enumContains(field.Enum, val) {
			failures = append(failures, &FieldFailure{
				Field:         name,
				Kind:          FailureEnumMismatch,
				Message:       fmt.Sprintf("field %q value %v is not in allowed set", name, val),
				Description:   field.Description,
				AllowedValues: field.Enum,
				ExpectedType:  field.Type,
				ActualType:    actual,
			})
		}
	}

	if len(failures) == 0 {
		return nil
	}
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Field == failures[j].Field {
			return failures[i].Kind < failures[j].Kind
		}
		return failures[i].Field < failures[j].Field
	})
	return &ValidationError{SectionPath: sectionPath, Failures: failures}
}

func valueMatchesType(t Type, v any) bool {
	switch t {
	case TypeString:
		_, ok := v.(string)
		return ok
	case TypeInteger:
		return isIntegerValue(v)
	case TypeFloat:
		return isFloatValue(v)
	case TypeBoolean:
		_, ok := v.(bool)
		return ok
	case TypeDatetime:
		return isDatetimeValue(v)
	case TypeArray:
		if v == nil {
			return false
		}
		k := reflect.ValueOf(v).Kind()
		return k == reflect.Slice || k == reflect.Array
	case TypeTable:
		if v == nil {
			return false
		}
		return reflect.ValueOf(v).Kind() == reflect.Map
	}
	return false
}

func isIntegerValue(v any) bool {
	switch n := v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		f := float64(n)
		return !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f)
	case float64:
		return !math.IsNaN(n) && !math.IsInf(n, 0) && n == math.Trunc(n)
	}
	return false
}

func isFloatValue(v any) bool {
	switch v.(type) {
	case float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func isDatetimeValue(v any) bool {
	switch x := v.(type) {
	case time.Time:
		return true
	case string:
		for _, layout := range []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05",
			"2006-01-02",
			"15:04:05",
		} {
			if _, err := time.Parse(layout, x); err == nil {
				return true
			}
		}
	}
	return false
}

func enumContains(allowed []any, v any) bool {
	for _, a := range allowed {
		if reflect.DeepEqual(a, v) {
			return true
		}
		if numericEqual(a, v) {
			return true
		}
	}
	return false
}

func numericEqual(a, b any) bool {
	af, aok := toFloat64(a)
	bf, bok := toFloat64(b)
	if !aok || !bok {
		return false
	}
	return af == bf
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func describeType(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
