package schema

// Type is the declared type of a schema field, matching TOML's native types.
// The string form is the wire representation in the schema config and in the
// JSON contract of *ValidationError.
type Type string

// Supported schema field types. Each value corresponds to a TOML native type.
const (
	// TypeString is a TOML basic or literal string.
	TypeString Type = "string"
	// TypeInteger is a TOML integer.
	TypeInteger Type = "integer"
	// TypeFloat is a TOML float.
	TypeFloat Type = "float"
	// TypeBoolean is a TOML boolean.
	TypeBoolean Type = "boolean"
	// TypeDatetime is a TOML datetime, accepted as time.Time or an RFC 3339
	// / date / time layout string.
	TypeDatetime Type = "datetime"
	// TypeArray is a TOML array, accepted as any Go slice or array.
	TypeArray Type = "array"
	// TypeTable is a TOML table, accepted as any Go map.
	TypeTable Type = "table"
)

// Field describes a single field within a SectionType.
type Field struct {
	// Name is the declared field name as it appears in section data.
	Name string
	// Type is the declared schema type; see the Type constants.
	Type Type
	// Required marks the field as mandatory during validation.
	Required bool
	// Description is surfaced to agents verbatim in validation failures.
	Description string
	// Enum, when non-empty, constrains the field's value to this set.
	Enum []any
	// Format is an optional format hint (e.g. "date-time") carried through
	// from the schema config; currently informational only.
	Format string
	// Default is the default value declared in the schema config. It is not
	// applied during validation; callers that want defaulting behaviour must
	// merge it in explicitly.
	Default any
}

// SectionType is a named collection of fields, e.g. "task" or "note". It
// corresponds to one entry in the schema config's [schema.<name>] table.
type SectionType struct {
	// Name is the section-type name, matching the first segment of each
	// concrete section path that resolves to this type.
	Name string
	// Description is the human-readable description from the schema config.
	Description string
	// Fields maps declared field name to its Field definition.
	Fields map[string]Field
}

// Registry is the resolved set of section types for a given TOML file.
// The zero value is valid and has no section types.
type Registry struct {
	// Types maps section-type name (e.g. "task") to its SectionType.
	Types map[string]SectionType
}

// Lookup returns the section type for the first segment of a section path.
// The path "task.task_001" resolves to the "task" type. The second return
// value is false when no matching section type is registered.
func (r Registry) Lookup(sectionPath string) (SectionType, bool) {
	name := firstSegment(sectionPath)
	t, ok := r.Types[name]
	return t, ok
}

func firstSegment(path string) string {
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			return path[:i]
		}
	}
	return path
}
