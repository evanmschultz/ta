package schema

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"
)

// ErrUnknownSectionType is returned by Validate when the first two
// segments of the section path do not resolve to a registered db+type.
// The wrapped message names the specific db or type segment that failed
// to resolve.
var ErrUnknownSectionType = errors.New("unknown section type")

// Validate checks data against the schema entry selected by sectionPath.
// sectionPath is the simple "<db>.<type>.<id>" form; the multi-instance
// "<db>.<instance>.<type>.<id>" form is resolved ahead of this call by
// the §12.3 address resolver. Validate returns nil if the data conforms,
// ErrUnknownSectionType wrapped with the offending segment if no schema
// is registered, or a *ValidationError aggregating every field-level
// failure.
func (r Registry) Validate(sectionPath string, data map[string]any) error {
	dbName, typeName, _ := splitFirstTwo(sectionPath)
	if dbName == "" {
		return fmt.Errorf("%w: empty section path", ErrUnknownSectionType)
	}
	db, ok := r.DBs[dbName]
	if !ok {
		return fmt.Errorf("%w: db %q not registered", ErrUnknownSectionType, dbName)
	}
	if typeName == "" {
		return fmt.Errorf("%w: missing type segment for db %q", ErrUnknownSectionType, dbName)
	}
	st, ok := db.Types[typeName]
	if !ok {
		return fmt.Errorf("%w: type %q not registered under db %q",
			ErrUnknownSectionType, typeName, dbName)
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
		failures = append(failures, validateField(name, field, val)...)
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

// validateField checks one field's value against its Field declaration,
// recursing into array elements when ElementType / ElementFields are
// declared. The path argument carries the bracketed/dotted accumulated
// field-path used for FieldFailure.Field. Top-level callers pass the
// bare field name; recursion appends "[i]" for array indices and
// "." + sub-name for nested table fields. All emitted failures use the
// fully-qualified path so agents can locate the offending leaf without
// re-walking the data.
func validateField(path string, field Field, val any) []*FieldFailure {
	actual := describeType(val)
	if !valueMatchesType(field.Type, val) {
		return []*FieldFailure{{
			Field:        path,
			Kind:         FailureTypeMismatch,
			Message:      fmt.Sprintf("field %q has type %q, expected %q", path, actual, field.Type),
			Description:  field.Description,
			ExpectedType: field.Type,
			ActualType:   actual,
		}}
	}
	if len(field.Enum) > 0 && !enumContains(field.Enum, val) {
		return []*FieldFailure{{
			Field:         path,
			Kind:          FailureEnumMismatch,
			Message:       fmt.Sprintf("field %q value %v is not in allowed set", path, val),
			Description:   field.Description,
			AllowedValues: field.Enum,
			ExpectedType:  field.Type,
			ActualType:    actual,
		}}
	}
	// Array recursion: walk each element against ElementType (when set)
	// and ElementFields (when set). An empty array is valid — zero
	// elements means zero per-element failures.
	if field.Type == TypeArray && (field.ElementType != "" || len(field.ElementFields) > 0) {
		return validateArrayElements(path, field, val)
	}
	// Direct nested-table recursion (F28): walk the table's runtime
	// value against each declared sub-field. Path format is
	// "<field>.<sub>" — no bracket token, since this is a single
	// table value rather than an array element. The same helper that
	// drives array-element table walks (validateNestedTable) handles
	// missing-required + per-sub-field validation + unknown-key
	// detection; only the path prefix differs.
	if field.Type == TypeTable && len(field.Fields) > 0 {
		return validateNestedTable(path, field.Fields, val)
	}
	return nil
}

// validateArrayElements walks every element of val (which has already
// matched TypeArray) against the field's per-element constraints.
// Failure paths use the bracket form: "<path>[<i>]". Element-fields
// recursion produces "<path>[<i>].<sub>" paths via validateField.
func validateArrayElements(path string, field Field, val any) []*FieldFailure {
	rv := reflect.ValueOf(val)
	if !(rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		// Belt-and-suspenders: caller already type-checked, but if the
		// element walker is reached on a non-array we surface a clean
		// type-mismatch on the parent rather than panicking.
		return []*FieldFailure{{
			Field:        path,
			Kind:         FailureTypeMismatch,
			Message:      fmt.Sprintf("field %q has type %q, expected %q", path, describeType(val), TypeArray),
			ExpectedType: TypeArray,
			ActualType:   describeType(val),
		}}
	}
	var out []*FieldFailure
	for i := range rv.Len() {
		elemPath := fmt.Sprintf("%s[%d]", path, i)
		elem := rv.Index(i).Interface()

		switch field.ElementType {
		case TypeTable:
			// Table element: must be map; ElementFields constrains the
			// per-element shape (missing required, type mismatch on
			// sub-fields, unknown sub-fields).
			if !valueMatchesType(TypeTable, elem) {
				out = append(out, &FieldFailure{
					Field:        elemPath,
					Kind:         FailureTypeMismatch,
					Message:      fmt.Sprintf("field %q has type %q, expected %q", elemPath, describeType(elem), TypeTable),
					ExpectedType: TypeTable,
					ActualType:   describeType(elem),
				})
				continue
			}
			if len(field.ElementFields) > 0 {
				out = append(out, validateNestedTable(elemPath, field.ElementFields, elem)...)
			}
		case "":
			// No element_type declared: nothing more to check past the
			// outer array type-check.
		default:
			// Primitive element: type-check each leaf.
			if !valueMatchesType(field.ElementType, elem) {
				out = append(out, &FieldFailure{
					Field:        elemPath,
					Kind:         FailureTypeMismatch,
					Message:      fmt.Sprintf("field %q has type %q, expected %q", elemPath, describeType(elem), field.ElementType),
					Description:  field.Description,
					ExpectedType: field.ElementType,
					ActualType:   describeType(elem),
				})
			}
		}
	}
	return out
}

// validateNestedTable runs missing-required + per-sub-field checks on
// one table value, parameterized by the path prefix every sub-field
// failure inherits. Two callers, one helper:
//
//   - Array-of-tables sub-field walk (`element_fields`): caller passes
//     `<field>[<i>]`; failures read `<field>[<i>].<sub>`.
//   - Direct nested-table sub-field walk (`fields`, F28): caller passes
//     the bare `<field>`; failures read `<field>.<sub>`.
//
// The dotted concatenation here is identical for both callers — only
// the prefix changes.
func validateNestedTable(pathPrefix string, fields map[string]Field, val any) []*FieldFailure {
	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Map {
		return []*FieldFailure{{
			Field:        pathPrefix,
			Kind:         FailureTypeMismatch,
			Message:      fmt.Sprintf("field %q has type %q, expected %q", pathPrefix, describeType(val), TypeTable),
			ExpectedType: TypeTable,
			ActualType:   describeType(val),
		}}
	}
	// Materialize map into map[string]any for uniform key access.
	flat := make(map[string]any, rv.Len())
	for _, k := range rv.MapKeys() {
		ks, ok := k.Interface().(string)
		if !ok {
			return []*FieldFailure{{
				Field:   pathPrefix,
				Kind:    FailureTypeMismatch,
				Message: fmt.Sprintf("field %q has non-string map key %v", pathPrefix, k.Interface()),
			}}
		}
		flat[ks] = rv.MapIndex(k).Interface()
	}

	var out []*FieldFailure
	// Missing required.
	for _, fname := range sortedKeys(fields) {
		sub := fields[fname]
		if !sub.Required {
			continue
		}
		if _, present := flat[fname]; present {
			continue
		}
		subPath := pathPrefix + "." + fname
		out = append(out, &FieldFailure{
			Field:         subPath,
			Kind:          FailureMissingRequired,
			Message:       fmt.Sprintf("missing required field %q", subPath),
			Description:   sub.Description,
			AllowedValues: sub.Enum,
			ExpectedType:  sub.Type,
		})
	}
	// Per-sub-field validation (type/enum/element recursion) and unknown
	// sub-field detection.
	for _, fname := range sortedKeys(flat) {
		sub, known := fields[fname]
		subPath := pathPrefix + "." + fname
		if !known {
			out = append(out, &FieldFailure{
				Field:   subPath,
				Kind:    FailureUnknownField,
				Message: fmt.Sprintf("unknown field %q", subPath),
			})
			continue
		}
		out = append(out, validateField(subPath, sub, flat[fname])...)
	}
	return out
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
