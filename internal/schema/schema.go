package schema

import (
	"maps"
	"strings"
)

// SingleFileMount reports whether mount is a single-file mount entry —
// one that resolves to exactly one concrete file at expansion time.
// Per F10 (PLAN §12.17.9), a mount is single-file when it has no glob
// `*` and is not collection-shaped (collection mounts are rejected at
// schema-load time so this condition only ever holds during schema
// loading itself for the rejection path). Examples:
//
//   - "plans.toml"              → single-file.
//   - "README.md"               → single-file.
//   - "docs/api.md"             → single-file.
//   - "workflow/*/db.toml"      → multi-file (glob).
//
// Post-F10 the bracket form is uniform (`[<id>]` verbatim), so this
// helper no longer drives bracket-form selection. It survives because
// the index walker still needs to know whether a db has exactly one
// concrete backing file (single-file dbs index the bare bracket as
// the id; multi-file dbs prepend the file-relpath to form the id).
func SingleFileMount(mount string) bool {
	if mount == "." {
		return false
	}
	if strings.Contains(mount, "*") {
		return false
	}
	if strings.HasSuffix(mount, "/") {
		return false
	}
	return true
}

// IsSingleFileDB reports whether db is declared with a single-entry
// Paths slice that itself names a single concrete file (no glob).
// Used by the index walker to decide whether the on-disk bracket
// already IS the full id (single-file: yes) or whether the
// file-relpath must be prepended (multi-file: yes).
func IsSingleFileDB(db DB) bool {
	if len(db.Paths) != 1 {
		return false
	}
	return SingleFileMount(db.Paths[0])
}

// DBHasFileAsRecord reports whether any declared type on db carries
// RecordPer == RecordPerFile. The id-grammar relaxation in F31
// (file-as-record dbs accept ids equal to the file-relpath alone, no
// bracket-key) gates on this — section-only dbs keep the strict
// bracket-key requirement.
func DBHasFileAsRecord(db DB) bool {
	for _, t := range db.Types {
		if t.IsFileRecord() {
			return true
		}
	}
	return false
}

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

// RecordPer values determine the on-disk record granularity for an MD
// type per F31. Section-mode (the default) chops one file into many
// records via ATX heading boundaries. File-mode treats the whole file
// as one record with YAML frontmatter holding the typed fields and the
// body holding the markdown content under a single declared
// `body_field`.
const (
	// RecordPerSection is the default — one MD file may host multiple
	// records, each anchored at an ATX heading. Pre-F31 behavior.
	RecordPerSection = "section"
	// RecordPerFile makes the whole file one record (file-as-record);
	// frontmatter holds typed fields, body holds the markdown under
	// `body_field`. Per F31.
	RecordPerFile = "file"
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
	// ElementType is the per-element type of an array field. When Type
	// is "array" and ElementType is non-empty, validation walks each
	// array element against this type. The literal "table" pairs with
	// ElementFields to declare per-element table shape; any other value
	// must be one of the seven primitives or a registered alias name
	// (which the loader inlines as ElementType + ElementFields before
	// validation runs). Empty when no per-element constraint is declared.
	ElementType Type
	// ElementFields is the per-element table shape used when
	// ElementType == "table". Each entry is itself a Field, so nesting
	// is unbounded. Empty when ElementType is a primitive or unset.
	ElementFields map[string]Field
	// Fields is the inner-shape declaration for a direct (non-array)
	// nested table field, populated when Type == "table" and the
	// schema declares per-instance sub-fields inline via
	// `[<...>.fields.<f>.fields.<sub>]`. Each entry is itself a
	// Field, so nesting is unbounded — TOML's bracket grammar is
	// the practical depth limit (no cap; cycle detection in alias
	// inlining covers correctness because each nested-table path is
	// declared inline and unique by construction). Empty when no
	// inner shape is declared (any-shape map). Per F28.
	Fields map[string]Field
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
	// AutoSpawn is the ordered list of child-record specs to fire on
	// successful Create of a record of this type. Empty / nil when no
	// `[<db>.<type>.auto_spawn]` block is declared (no-op). Order is
	// significant — `{index}` token interpolation uses 1-based position
	// within this slice, and on-disk write order follows declaration
	// order. Bases that declare `auto_spawn` propagate onto inheriting
	// concrete types via the same wholesale-replace rule as `fields` —
	// a concrete type's own auto_spawn overrides the inherited one. Per
	// F23.
	AutoSpawn []SpawnSpec
	// RecordPer is the on-disk record granularity for MD types
	// (RecordPerSection or RecordPerFile). Empty string is treated as
	// RecordPerSection for back-compat with pre-F31 schemas. TOML
	// types must leave this empty — only MD types support file mode.
	// Per F31.
	RecordPer string
	// BodyField names the field that receives the markdown body for
	// file-as-record types (RecordPer == RecordPerFile). Required on
	// file-as-record types; forbidden everywhere else. The named field
	// must exist on the type's resolved Fields map. Per F31.
	BodyField string
}

// IsFileRecord reports whether st is a file-as-record type per F31.
// Section-mode is the default (empty RecordPer === RecordPerSection).
func (st SectionType) IsFileRecord() bool {
	return st.RecordPer == RecordPerFile
}

// SpawnSpec is one auto-spawn child-record specification, declared as
// an entry in `on_create = [...]` under `[<db>.<type>.auto_spawn]`.
// Per F23.
type SpawnSpec struct {
	// Type is the db-qualified target type for the spawned record (e.g.
	// `plans.qa_proof`). Must resolve at load time to a concrete record
	// type — bases and aliases are rejected with ErrSpawnUnknownType.
	Type string
	// IDTemplate is the id literal with `{parent_id}` and `{index}`
	// interpolation tokens. Other tokens are rejected at load with
	// ErrSpawnInvalidIDTemplate. Empty templates are rejected.
	IDTemplate string
	// Fields is the static field-value table for the spawned record.
	// String entries are interpolated through the same two-token rule.
	// Missing required fields without defaults on the target type
	// surface as ErrSpawnIncompletePayload at load and at create time.
	Fields map[string]any
}

// DB is one database declared at the [<db>] root of a schema file. It
// carries the db-scope meta-fields (paths, description) plus the map
// of record types declared under it.
//
// Per F10 (PLAN §12.17.9), Format is INFERRED from the file extension
// on each Paths entry; it is not declared in the schema file. All
// entries within one db must share the same recognized extension.
type DB struct {
	// Name is the db name, matching the first segment of each concrete
	// section path that resolves to this db.
	Name string
	// Description is the human-readable description from [<db>].
	Description string
	// Paths is the declared list of mount paths for this db. Length 1+.
	// Glob `*` allowed for one segment per entry. See PLAN §12.17.9.
	Paths []string
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
