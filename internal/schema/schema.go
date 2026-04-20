package schema

// Type is the declared type of a schema field, matching TOML's native types.
type Type string

// Supported schema field types.
const (
	TypeString   Type = "string"
	TypeInteger  Type = "integer"
	TypeFloat    Type = "float"
	TypeBoolean  Type = "boolean"
	TypeDatetime Type = "datetime"
	TypeArray    Type = "array"
	TypeTable    Type = "table"
)

// Field describes a single field within a section type.
type Field struct {
	Name        string
	Type        Type
	Required    bool
	Description string
	Enum        []any
	Format      string
	Default     any
}

// SectionType is a named collection of fields, e.g. "task" or "note".
type SectionType struct {
	Name        string
	Description string
	Fields      map[string]Field
}

// Registry is the resolved set of section types for a given TOML file.
// The zero value is valid and has no section types.
type Registry struct {
	Types map[string]SectionType
}

// Lookup returns the section type for the first segment of a section path.
// The path "task.task_001" resolves to the "task" type.
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
