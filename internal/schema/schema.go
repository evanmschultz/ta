package schema

import (
	"maps"
	"strings"
)

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

// Shape names the db shape selector at the [<db>] root: exactly one of
// `file`, `directory`, or `collection` is set. See V2-PLAN §4.1 and §5.5.
type Shape string

// Supported db shape selectors. Each matches one of the three mutually
// exclusive root keys allowed on a [<db>] block.
const (
	// ShapeFile marks a single-instance db backed by one file at the
	// declared relative path (e.g. "plans.toml", "README.md").
	ShapeFile Shape = "file"
	// ShapeDirectory marks a dir-per-instance db: every immediate subdir
	// under the declared directory that contains the canonical db file is
	// one instance.
	ShapeDirectory Shape = "directory"
	// ShapeCollection marks a file-per-instance db: every file under the
	// declared directory (recursively) whose extension matches `format`
	// is one instance.
	ShapeCollection Shape = "collection"
)

// Format names the canonical on-disk format of a db's records. Exactly one
// backend handles each Format.
type Format string

// Supported db formats.
const (
	// FormatTOML selects the TOML backend (internal/backend/toml).
	FormatTOML Format = "toml"
	// FormatMD selects the Markdown backend (internal/backend/md, §12.4).
	FormatMD Format = "md"
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
	// Format is an optional format hint (e.g. "markdown") carried through
	// from the schema config; currently informational only.
	Format string
	// Default is the default value declared in the schema config. It is not
	// applied during validation; callers that want defaulting behaviour must
	// merge it in explicitly.
	Default any
}

// SectionType is a named collection of fields, e.g. "build_task" or
// "section". It corresponds to one entry in the schema config's
// [<db>.<type>] table.
type SectionType struct {
	// Name is the section-type name, matching the second segment of each
	// concrete section path that resolves to this type.
	Name string
	// Description is the human-readable description from the schema config.
	Description string
	// Heading is the MD heading level (1..6) this type's records occupy.
	// Zero for TOML dbs; required for MD dbs per §4.7.
	Heading int
	// Fields maps declared field name to its Field definition.
	Fields map[string]Field
}

// DB is one database declared at the [<db>] root of a schema file. It
// carries the db-scope meta-fields (shape selector, format, heading) plus
// the map of record types declared under it.
type DB struct {
	// Name is the db name, matching the first segment of each concrete
	// section path that resolves to this db.
	Name string
	// Description is the human-readable description from [<db>].
	Description string
	// Shape is the selector of which root key is set: file, directory, or
	// collection.
	Shape Shape
	// Path is the value of whichever root key Shape names (e.g. "plans.toml"
	// for ShapeFile, "workflow" for ShapeDirectory, "docs" for
	// ShapeCollection). Always relative to the project root.
	Path string
	// Format is the canonical on-disk format. TOML or MD.
	Format Format
	// Types maps record-type name (second segment of an address) to its
	// SectionType.
	Types map[string]SectionType
}

// Registry is the resolved set of databases for a given project. The zero
// value is valid and has no dbs.
type Registry struct {
	// DBs maps db name (first segment of an address, e.g. "plan_db") to
	// its declaration.
	DBs map[string]DB
}

// Lookup returns the section type named by the first two segments of a
// section path. The path "plan_db.build_task.task_001" resolves to the
// "build_task" SectionType under the "plan_db" DB. The second return value
// is false when either the db or the type is not registered.
//
// NOTE: Lookup assumes the simple <db>.<type>.<id> address form. The
// multi-instance <db>.<instance>.<type>.<id> form belongs to the address
// resolver in §12.3 and is not handled here.
func (r Registry) Lookup(sectionPath string) (SectionType, bool) {
	dbName, typeName, _ := splitFirstTwo(sectionPath)
	if dbName == "" || typeName == "" {
		return SectionType{}, false
	}
	db, ok := r.DBs[dbName]
	if !ok {
		return SectionType{}, false
	}
	t, ok := db.Types[typeName]
	return t, ok
}

// LookupDB returns the DB named by the first segment of a section path.
// The second return value is false when no matching db is registered.
func (r Registry) LookupDB(sectionPath string) (DB, bool) {
	name := firstSegment(sectionPath)
	db, ok := r.DBs[name]
	return db, ok
}

// Override returns a new Registry containing every DB from r, with
// same-named DBs from other replacing r's entries (wholesale; §4.4), and
// DBs unique to either retained. Neither r nor other is mutated.
//
// This is the cascade-merge primitive: callers walk the config chain from
// base (home) to most-specific (closest to the target file) and fold each
// loaded Registry with accumulator = accumulator.Override(loaded).
func (r Registry) Override(other Registry) Registry {
	merged := Registry{DBs: make(map[string]DB, len(r.DBs)+len(other.DBs))}
	maps.Copy(merged.DBs, r.DBs)
	maps.Copy(merged.DBs, other.DBs)
	return merged
}

func firstSegment(path string) string {
	before, _, _ := strings.Cut(path, ".")
	return before
}

// splitFirstTwo returns the first and second dot-separated segments of
// path plus the remainder. All three are empty strings when the
// corresponding segment is not present.
func splitFirstTwo(path string) (first, second, rest string) {
	first, after, ok := strings.Cut(path, ".")
	if !ok {
		return first, "", ""
	}
	second, rest, _ = strings.Cut(after, ".")
	return first, second, rest
}
