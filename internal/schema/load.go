package schema

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Meta-field keys recognised at the [<db>] root. Any other key at that
// level that is not a sub-table is a meta-schema violation.
//
// Per F10 (PLAN §12.17.9): `format` is NOT a recognized meta-field —
// it is inferred from the file extension on each path. Any input
// containing `format =` errors via the standard unknown-key path.
//
// Per F21: `types` is reserved at the db level for the named-alias
// table ([<db>.types.<alias>]); it is therefore not a valid record
// type name.
const (
	metaFieldPaths       = "paths"
	metaFieldDescription = "description"
	metaFieldTypes       = "types"
)

// Field-level keys recognised inside a [<db>.<type>.fields.<name>] table.
const (
	fieldKeyType          = "type"
	fieldKeyRequired      = "required"
	fieldKeyDescription   = "description"
	fieldKeyEnum          = "enum"
	fieldKeyFormat        = "format"
	fieldKeyDefault       = "default"
	fieldKeyElementType   = "element_type"
	fieldKeyElementFields = "element_fields"
)

// Type-level keys recognised on a [<db>.<type>] table (alongside the
// reserved `fields` sub-table).
const (
	typeKeyDescription = "description"
	typeKeyHeading     = "heading"
	typeKeyFields      = "fields"
)

// Sentinel errors per F10 (PLAN §12.17.9).
var (
	// ErrCollectionMountUnsupported is returned when a path declaration
	// uses a trailing-slash collection root (`docs/`) or the `.`
	// project-root mount. Use globs (`docs/*.md`) instead.
	ErrCollectionMountUnsupported = errors.New(
		"schema: collection mounts (trailing-slash or `.`) are not supported; use a glob like `docs/*.md`")

	// ErrInconsistentPathFormats is returned when a db's paths slice
	// contains entries with different recognized extensions (mix of
	// .toml and .md, etc.).
	ErrInconsistentPathFormats = errors.New(
		"schema: paths within one db must share a recognized extension")

	// ErrAmbiguousPathFormat is returned when a path entry has no
	// recognized extension (`paths = ["plans"]`) and so the format
	// cannot be inferred.
	ErrAmbiguousPathFormat = errors.New(
		"schema: path entry must have a recognized extension (.toml or .md)")

	// ErrIDCollisionAcrossTypes is returned at registry-build time
	// when a single-file db declares multiple types and the schema
	// loader cannot prove id uniqueness statically. (CRUD operations
	// re-check at write time.)
	ErrIDCollisionAcrossTypes = errors.New(
		"schema: id collision across types in single-file db")

	// ErrOverlappingPaths is returned when two distinct dbs declare any
	// overlapping entries in their `paths` slices.
	ErrOverlappingPaths = errors.New("schema: overlapping paths across dbs")

	// ErrUnknownElementType is returned when a field's element_type
	// names neither a primitive (string/integer/float/boolean/datetime/
	// table) nor a registered alias under any [<db>.types.<alias>].
	// "array" specifically is rejected as nested arrays are not
	// supported in v1; the message in that case still wraps this
	// sentinel for uniform error-class testing.
	ErrUnknownElementType = errors.New("schema: unknown element_type")

	// ErrAliasCycle is returned when alias-resolution detects a cycle
	// (self-reference or mutual: A → B → A). The error message
	// includes the full chain for debugging.
	ErrAliasCycle = errors.New("schema: type alias cycle")

	// ErrAliasShadowsPrimitive is returned when an alias is declared
	// with a name that matches one of the seven reserved primitive
	// type names (string, integer, float, boolean, datetime, array,
	// table). Allowing the shadow would make `element_type = "string"`
	// ambiguous (alias vs primitive) so the loader rejects up front.
	ErrAliasShadowsPrimitive = errors.New("schema: alias shadows reserved primitive type")
)

// formatFromPath returns the format inferred from path's file
// extension. Returns ("", false) when the extension is not recognized
// or the path is collection-shaped.
func formatFromPath(path string) (Format, bool) {
	ext := filepath.Ext(path)
	switch strings.ToLower(ext) {
	case ".toml":
		return FormatTOML, true
	case ".md":
		return FormatMD, true
	}
	return "", false
}

// Load reads a schema config document from r and returns the resolved
// Registry.
func Load(r io.Reader) (Registry, error) {
	dec := toml.NewDecoder(r)

	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return Registry{}, fmt.Errorf("schema: parse config: %w", err)
	}
	return buildRegistry(raw)
}

// LoadBytes is the byte-slice convenience wrapper for Load.
func LoadBytes(buf []byte) (Registry, error) {
	var raw map[string]any
	if err := toml.Unmarshal(buf, &raw); err != nil {
		return Registry{}, fmt.Errorf("schema: parse config: %w", err)
	}
	return buildRegistry(raw)
}

func buildRegistry(raw map[string]any) (Registry, error) {
	reg := Registry{DBs: make(map[string]DB, len(raw))}

	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)

	// Phase A — collect alias declarations. Each [<db>.types.<alias>]
	// block is a record-type-shaped body (description + fields map)
	// stashed in a Registry-wide alias table. Aliases at this stage
	// retain raw element_type strings; alias-to-alias chains and
	// alias-references inside alias bodies are resolved transitively
	// by resolveAlias during phase B.
	aliasRaw := map[string]map[string]Field{}

	for _, name := range names {
		bodyAny := raw[name]
		body, ok := bodyAny.(map[string]any)
		if !ok {
			return Registry{}, fmt.Errorf(
				"schema: %s: top-level entry must be a table, got %T", name, bodyAny)
		}
		if err := collectAliases(name, body, aliasRaw); err != nil {
			return Registry{}, err
		}
	}

	// Phase A.5 — build dbs / types / fields. element_type / element_fields
	// are recorded as-declared; alias names appear verbatim in ElementType
	// and are inlined in phase B.
	for _, name := range names {
		body := raw[name].(map[string]any)
		db, err := buildDB(name, body)
		if err != nil {
			return Registry{}, err
		}
		reg.DBs[name] = db
	}

	// Phase B — expand alias references throughout the registry. Aliases
	// referring to other aliases resolve transitively; cycles are caught
	// via a per-walk visiting set.
	if err := expandAliases(reg, aliasRaw); err != nil {
		return Registry{}, err
	}

	if err := checkPathsOverlap(reg); err != nil {
		return Registry{}, err
	}
	return reg, nil
}

// collectAliases walks one db body for its [<db>.types.<alias>] entries,
// building each alias body's `fields.<name>` table into a map of Field
// values. Aliases share a flat Registry-wide namespace so duplicates
// across dbs are rejected. The bare `description = "..."` key on
// [<db>.types] (documentation only) is permitted and ignored.
func collectAliases(dbName string, body map[string]any, dst map[string]map[string]Field) error {
	tBody, ok := body[metaFieldTypes]
	if !ok {
		return nil
	}
	typesBody, ok := tBody.(map[string]any)
	if !ok {
		return fmt.Errorf(
			"schema: %s.%s: must be a table of named alias declarations, got %T",
			dbName, metaFieldTypes, tBody)
	}
	aliasNames := make([]string, 0, len(typesBody))
	for n := range typesBody {
		aliasNames = append(aliasNames, n)
	}
	sort.Strings(aliasNames)
	for _, alias := range aliasNames {
		val := typesBody[alias]
		if alias == typeKeyDescription {
			// Allow a bare description string at [<db>.types] for
			// documentation; not an alias.
			if _, isStr := val.(string); !isStr {
				return fmt.Errorf(
					"schema: %s.%s.description: must be string, got %T",
					dbName, metaFieldTypes, val)
			}
			continue
		}
		ab, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf(
				"schema: %s.%s.%s: alias body must be a table, got %T",
				dbName, metaFieldTypes, alias, val)
		}
		if isReservedPrimitive(alias) {
			return fmt.Errorf(
				"%w: %q (declared at %s.%s.%s)",
				ErrAliasShadowsPrimitive, alias, dbName, metaFieldTypes, alias)
		}
		if _, dup := dst[alias]; dup {
			return fmt.Errorf(
				"schema: %s.%s.%s: alias %q already declared (aliases share a Registry-wide namespace)",
				dbName, metaFieldTypes, alias, alias)
		}
		fieldsMap, err := buildAliasFields(dbName, alias, ab)
		if err != nil {
			return err
		}
		dst[alias] = fieldsMap
	}
	return nil
}

// buildAliasFields parses one [<db>.types.<alias>] body. The body
// follows the same shape as a record type ([<db>.<type>]): description
// (optional) and a fields map whose entries are field bodies built via
// buildField. The returned map is the alias's per-element Field set.
func buildAliasFields(dbName, alias string, body map[string]any) (map[string]Field, error) {
	scope := dbName + "." + metaFieldTypes + "." + alias
	var fieldsBody map[string]any
	for key, val := range body {
		switch key {
		case typeKeyDescription:
			if _, ok := val.(string); !ok {
				return nil, fmt.Errorf(
					"schema: %s.description: must be string, got %T", scope, val)
			}
		case typeKeyFields:
			fb, ok := val.(map[string]any)
			if !ok {
				return nil, fmt.Errorf(
					"schema: %s.fields: must be a table, got %T", scope, val)
			}
			fieldsBody = fb
		default:
			return nil, fmt.Errorf(
				"schema: %s: unknown key %q (allowed: description, fields)",
				scope, key)
		}
	}
	if len(fieldsBody) == 0 {
		return nil, fmt.Errorf(
			"schema: %s: alias must declare at least one field under [%s.fields]",
			scope, scope)
	}
	out := make(map[string]Field, len(fieldsBody))
	for fname, fval := range fieldsBody {
		fbody, ok := fval.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"schema: %s.fields.%s: must be a table, got %T",
				scope, fname, fval)
		}
		f, err := buildField(dbName, metaFieldTypes+"."+alias, fname, fbody)
		if err != nil {
			return nil, err
		}
		out[fname] = f
	}
	return out, nil
}

// expandAliases walks every field in reg, inlining alias references in
// fields whose ElementType names an alias. Aliases that themselves
// reference other aliases resolve transitively; cycles (self-reference
// or A → B → A) surface as ErrAliasCycle.
func expandAliases(reg Registry, aliasRaw map[string]map[string]Field) error {
	resolved := map[string]map[string]Field{}
	for name := range aliasRaw {
		visiting := map[string]bool{}
		out, err := resolveAlias(name, aliasRaw, resolved, visiting, nil)
		if err != nil {
			return err
		}
		resolved[name] = out
	}

	for dbName, db := range reg.DBs {
		for tName, st := range db.Types {
			for fName, f := range st.Fields {
				out, err := inlineField(f, resolved)
				if err != nil {
					return fmt.Errorf(
						"schema: %s.%s.fields.%s: %w", dbName, tName, fName, err)
				}
				st.Fields[fName] = out
			}
			db.Types[tName] = st
		}
		reg.DBs[dbName] = db
	}
	return nil
}

// resolveAlias returns the fully-resolved per-element Field map for
// alias `name`. Resolution recurses through alias-to-alias references
// inside the alias body's own fields. `visiting` tracks names on the
// current recursion stack for cycle detection; `chain` is the ordered
// list of names traversed (used in human-readable cycle messages).
func resolveAlias(
	name string,
	raw map[string]map[string]Field,
	resolved map[string]map[string]Field,
	visiting map[string]bool,
	chain []string,
) (map[string]Field, error) {
	if out, done := resolved[name]; done {
		return out, nil
	}
	body, ok := raw[name]
	if !ok {
		return nil, fmt.Errorf(
			"%w: alias %q referenced but not declared", ErrUnknownElementType, name)
	}
	if visiting[name] {
		return nil, fmt.Errorf(
			"%w: %s → %s",
			ErrAliasCycle, strings.Join(append(chain, name), " → "), name)
	}
	visiting[name] = true
	defer delete(visiting, name)
	chain = append(chain, name)

	out := make(map[string]Field, len(body))
	for fname, f := range body {
		expanded, err := inlineFieldRecursive(f, raw, resolved, visiting, chain)
		if err != nil {
			return nil, err
		}
		out[fname] = expanded
	}
	return out, nil
}

// inlineField is the post-resolution inliner used on every record-type
// field in the registry. Alias references resolve via the resolved map
// (no recursion needed because aliases are already fully expanded).
func inlineField(f Field, resolved map[string]map[string]Field) (Field, error) {
	return inlineFieldRecursive(f, nil, resolved, map[string]bool{}, nil)
}

// inlineFieldRecursive walks one Field. When ElementType names an alias,
// the field's ElementType becomes "table" and ElementFields adopts a
// deep-clone of the alias's resolved fields. When ElementFields is
// already populated, each entry recurses (so nested alias references
// inside element_fields also resolve). The raw alias map is non-nil
// only when called transitively from resolveAlias (so alias bodies
// referencing other aliases get expanded on demand).
func inlineFieldRecursive(
	f Field,
	rawAliases map[string]map[string]Field,
	resolved map[string]map[string]Field,
	visiting map[string]bool,
	chain []string,
) (Field, error) {
	if f.Type == TypeArray && f.ElementType != "" {
		et := string(f.ElementType)
		switch {
		case et == string(TypeArray):
			return Field{}, fmt.Errorf(
				"%w: element_type = \"array\" (nested arrays are not supported in v1)",
				ErrUnknownElementType)
		case isReservedPrimitive(et):
			// Primitive (string/integer/float/boolean/datetime/table).
			// No alias inlining needed; ElementFields recursion below
			// still applies for table elements.
		default:
			var aliasBody map[string]Field
			if rawAliases != nil {
				ab, err := resolveAlias(et, rawAliases, resolved, visiting, chain)
				if err != nil {
					return Field{}, err
				}
				aliasBody = ab
			} else {
				ab, ok := resolved[et]
				if !ok {
					return Field{}, fmt.Errorf(
						"%w: %q (not a primitive, not a registered alias)",
						ErrUnknownElementType, et)
				}
				aliasBody = ab
			}
			f.ElementType = TypeTable
			f.ElementFields = cloneFieldMap(aliasBody)
		}
	}
	if len(f.ElementFields) > 0 {
		out := make(map[string]Field, len(f.ElementFields))
		for k, sub := range f.ElementFields {
			expanded, err := inlineFieldRecursive(sub, rawAliases, resolved, visiting, chain)
			if err != nil {
				return Field{}, err
			}
			out[k] = expanded
		}
		f.ElementFields = out
	}
	return f, nil
}

// cloneFieldMap returns a deep copy of a Field map. Used to make sure
// alias inlining never produces aliasing maps shared between
// independent fields.
func cloneFieldMap(in map[string]Field) map[string]Field {
	if in == nil {
		return nil
	}
	out := make(map[string]Field, len(in))
	for k, v := range in {
		out[k] = cloneField(v)
	}
	return out
}

// cloneField returns a deep copy of f including ElementFields. The
// shared Enum []any is intentionally aliased — validation never
// mutates it.
func cloneField(f Field) Field {
	out := f
	out.ElementFields = cloneFieldMap(f.ElementFields)
	return out
}

func isReservedPrimitive(name string) bool {
	switch Type(name) {
	case TypeString, TypeInteger, TypeFloat, TypeBoolean,
		TypeDatetime, TypeArray, TypeTable:
		return true
	}
	return false
}

func buildDB(name string, body map[string]any) (DB, error) {
	db := DB{Name: name, Types: map[string]SectionType{}}

	for key, val := range body {
		switch key {
		case metaFieldPaths:
			paths, err := stringSliceVal(name, key, val)
			if err != nil {
				return DB{}, err
			}
			db.Paths = paths
		case metaFieldDescription:
			s, err := stringVal(name, key, val)
			if err != nil {
				return DB{}, err
			}
			db.Description = s
		case metaFieldTypes:
			// Reserved per F21 for the named-alias table
			// [<db>.types.<alias>]. Aliases are collected up front by
			// collectAliases; here we just verify the shape is a table
			// and skip — alias bodies are not record types.
			if _, ok := val.(map[string]any); !ok {
				return DB{}, fmt.Errorf(
					"schema: %s.%s: must be a table of named alias declarations, got %T",
					name, key, val)
			}
		default:
			// Must be a record-type sub-table. Any scalar / non-table
			// value at this level (e.g. `format = "toml"`, the legacy
			// `file` / `directory` / `collection` keys) is an unknown
			// meta-field per F10's unknown-key contract.
			typeBody, ok := val.(map[string]any)
			if !ok {
				return DB{}, fmt.Errorf(
					"schema: %s.%s: unknown meta-field or non-table value (type %T); "+
						"record types must be tables, meta-fields must be one of "+
						"paths/description/types (PLAN §12.17.9 F10; F21)",
					name, key, val)
			}
			st, err := buildType(name, key, typeBody)
			if err != nil {
				return DB{}, err
			}
			db.Types[key] = st
		}
	}

	if db.Paths == nil {
		return DB{}, fmt.Errorf(
			"schema: %s: missing required %q array", name, metaFieldPaths)
	}
	if len(db.Paths) == 0 {
		return DB{}, fmt.Errorf(
			"schema: %s: %q must declare at least one entry", name, metaFieldPaths)
	}

	// Format-from-extension inference + invariants per F10:
	//   - Collection mounts (trailing /, `.`) rejected outright.
	//   - Extensionless paths rejected outright.
	//   - All paths in one db must share the same recognized extension.
	var inferred Format
	for i, p := range db.Paths {
		if p == "" {
			return DB{}, fmt.Errorf(
				"schema: %s: %q[%d] is empty", name, metaFieldPaths, i)
		}
		if p == "." || strings.HasSuffix(p, "/") {
			return DB{}, fmt.Errorf(
				"%w: db %q path %q", ErrCollectionMountUnsupported, name, p)
		}
		f, ok := formatFromPath(p)
		if !ok {
			return DB{}, fmt.Errorf(
				"%w: db %q path %q (want .toml or .md)",
				ErrAmbiguousPathFormat, name, p)
		}
		if i == 0 {
			inferred = f
			continue
		}
		if f != inferred {
			return DB{}, fmt.Errorf(
				"%w: db %q paths declare both %q and %q",
				ErrInconsistentPathFormats, name, inferred, f)
		}
	}
	db.Format = inferred

	if db.Format == FormatMD {
		if err := checkMDHeadings(name, db.Types); err != nil {
			return DB{}, err
		}
	} else {
		for tname, t := range db.Types {
			if t.Heading != 0 {
				return DB{}, fmt.Errorf(
					"schema: %s.%s: heading only allowed when db format is %q",
					name, tname, FormatMD)
			}
		}
	}

	return db, nil
}

func buildType(db, name string, body map[string]any) (SectionType, error) {
	st := SectionType{Name: name, Fields: map[string]Field{}}

	for key, val := range body {
		switch key {
		case typeKeyDescription:
			s, err := stringVal(db+"."+name, key, val)
			if err != nil {
				return SectionType{}, err
			}
			st.Description = s
		case typeKeyHeading:
			n, err := intVal(db+"."+name, key, val)
			if err != nil {
				return SectionType{}, err
			}
			if n < 1 || n > 6 {
				return SectionType{}, fmt.Errorf(
					"schema: %s.%s: heading = %d invalid (must be 1..6)", db, name, n)
			}
			st.Heading = n
		case typeKeyFields:
			fieldsBody, ok := val.(map[string]any)
			if !ok {
				return SectionType{}, fmt.Errorf(
					"schema: %s.%s.fields: must be a table, got %T", db, name, val)
			}
			for fname, fval := range fieldsBody {
				fbody, ok := fval.(map[string]any)
				if !ok {
					return SectionType{}, fmt.Errorf(
						"schema: %s.%s.fields.%s: must be a table, got %T",
						db, name, fname, fval)
				}
				f, err := buildField(db, name, fname, fbody)
				if err != nil {
					return SectionType{}, err
				}
				st.Fields[fname] = f
			}
		default:
			return SectionType{}, fmt.Errorf(
				"schema: %s.%s: unknown key %q (allowed: description, heading, fields)",
				db, name, key)
		}
	}

	if st.Description == "" {
		return SectionType{}, fmt.Errorf(
			"schema: %s.%s: missing required %q", db, name, typeKeyDescription)
	}
	if len(st.Fields) == 0 {
		return SectionType{}, fmt.Errorf(
			"schema: %s.%s: type must declare at least one field", db, name)
	}
	return st, nil
}

func buildField(db, typeName, fname string, body map[string]any) (Field, error) {
	f := Field{Name: fname}
	scope := db + "." + typeName + ".fields." + fname
	for key, val := range body {
		switch key {
		case fieldKeyType:
			s, err := stringVal(scope, key, val)
			if err != nil {
				return Field{}, err
			}
			f.Type = Type(s)
		case fieldKeyRequired:
			b, ok := val.(bool)
			if !ok {
				return Field{}, fmt.Errorf(
					"schema: %s.required: must be boolean, got %T", scope, val)
			}
			f.Required = b
		case fieldKeyDescription:
			s, err := stringVal(scope, key, val)
			if err != nil {
				return Field{}, err
			}
			f.Description = s
		case fieldKeyEnum:
			arr, ok := val.([]any)
			if !ok {
				return Field{}, fmt.Errorf(
					"schema: %s.enum: must be array, got %T", scope, val)
			}
			f.Enum = arr
		case fieldKeyFormat:
			s, err := stringVal(scope, key, val)
			if err != nil {
				return Field{}, err
			}
			f.Format = s
		case fieldKeyDefault:
			f.Default = val
		case fieldKeyElementType:
			s, err := stringVal(scope, key, val)
			if err != nil {
				return Field{}, err
			}
			f.ElementType = Type(s)
		case fieldKeyElementFields:
			tbl, ok := val.(map[string]any)
			if !ok {
				return Field{}, fmt.Errorf(
					"schema: %s.element_fields: must be a table, got %T", scope, val)
			}
			subFields := make(map[string]Field, len(tbl))
			for sname, sval := range tbl {
				sbody, ok := sval.(map[string]any)
				if !ok {
					return Field{}, fmt.Errorf(
						"schema: %s.element_fields.%s: must be a table, got %T",
						scope, sname, sval)
				}
				sub, err := buildField(db, typeName, fname+".element_fields."+sname, sbody)
				if err != nil {
					return Field{}, err
				}
				subFields[sname] = sub
			}
			f.ElementFields = subFields
		default:
			return Field{}, fmt.Errorf(
				"schema: %s: unknown key %q (allowed: type, required, description, enum, format, default, element_type, element_fields)",
				scope, key)
		}
	}
	if f.Type == "" {
		return Field{}, fmt.Errorf(
			"schema: %s: missing required %q", scope, fieldKeyType)
	}
	if !isSupportedType(f.Type) {
		return Field{}, fmt.Errorf(
			"schema: %s: unsupported type %q", scope, f.Type)
	}
	// element_type / element_fields invariants:
	//   - element_type forbidden on non-array fields.
	//   - element_fields forbidden when element_type != "table".
	//   - element_type = "array" rejected (no nested arrays in v1).
	//   - When element_fields is present, element_type must be "table".
	if f.ElementType != "" && f.Type != TypeArray {
		return Field{}, fmt.Errorf(
			"schema: %s: element_type is only valid on type = \"array\" (got type %q)",
			scope, f.Type)
	}
	if f.ElementType == TypeArray {
		return Field{}, fmt.Errorf(
			"%w: %s: element_type = \"array\" (nested arrays are not supported in v1)",
			ErrUnknownElementType, scope)
	}
	if len(f.ElementFields) > 0 {
		if f.Type != TypeArray {
			return Field{}, fmt.Errorf(
				"schema: %s: element_fields is only valid on type = \"array\"", scope)
		}
		if f.ElementType != TypeTable {
			return Field{}, fmt.Errorf(
				"schema: %s: element_fields requires element_type = \"table\" (got %q)",
				scope, f.ElementType)
		}
	}
	// element_type validity: must be a primitive (excluding "array"), the
	// literal "table", or an alias name. Alias resolution happens in
	// phase B; here we just ensure non-empty values aren't garbage when
	// they happen to be primitives — the alias path is verified later.
	// (No early-return; phase B emits ErrUnknownElementType on the alias
	// path when the name is unregistered.)
	return f, nil
}

func checkMDHeadings(db string, types map[string]SectionType) error {
	seen := make(map[int]string, len(types))
	for name, t := range types {
		if t.Heading == 0 {
			return fmt.Errorf(
				"schema: %s.%s: MD types require %q (1..6)", db, name, typeKeyHeading)
		}
		if other, clash := seen[t.Heading]; clash {
			return fmt.Errorf(
				"schema: %s: heading %d shared by types %q and %q; each heading level must be unique per db",
				db, t.Heading, other, name)
		}
		seen[t.Heading] = name
	}
	return nil
}

// checkPathsOverlap enforces the cross-db invariant: no two dbs may
// share any entry in their `paths` slices.
func checkPathsOverlap(reg Registry) error {
	type entry struct {
		db   string
		path string
	}
	flat := make([]entry, 0)
	for name, db := range reg.DBs {
		for _, p := range db.Paths {
			flat = append(flat, entry{db: name, path: p})
		}
	}
	sort.Slice(flat, func(i, j int) bool {
		if flat[i].path != flat[j].path {
			return flat[i].path < flat[j].path
		}
		return flat[i].db < flat[j].db
	})
	for i := 0; i < len(flat); i++ {
		for j := i + 1; j < len(flat); j++ {
			if flat[i].db == flat[j].db {
				continue
			}
			if mountsOverlap(flat[i].path, flat[j].path) {
				return fmt.Errorf(
					"%w: dbs %q and %q both declare path %q (overlaps %q)",
					ErrOverlappingPaths, flat[i].db, flat[j].db,
					flat[i].path, flat[j].path)
			}
		}
	}
	return nil
}

// mountsOverlap reports whether two mount entries can resolve to any
// shared concrete file under the F10 grammar (no collection mounts;
// every path has a recognized extension).
func mountsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	reA := mountRegex(a)
	reB := mountRegex(b)
	if reA == nil || reB == nil {
		return a == b
	}
	return reA.MatchString(mountSample(b)) || reB.MatchString(mountSample(a))
}

// mountRegex compiles a mount entry into a regex anchored at both ends.
// `*` becomes `[^/]+`. Per F10, no collection-mount handling is
// required — they are rejected at schema-load.
func mountRegex(mount string) *regexp.Regexp {
	parts := strings.Split(mount, "/")
	for i, p := range parts {
		if p == "*" {
			parts[i] = `[^/]+`
		} else {
			parts[i] = regexp.QuoteMeta(p)
		}
	}
	pattern := "^" + strings.Join(parts, "/") + "$"
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

// mountSample returns a representative concrete-path expansion of mount
// suitable as input to another mount's regex. Globs expand to a literal
// sentinel segment ("x"); a non-collection mount with no `*` returns
// itself.
func mountSample(mount string) string {
	body := strings.ReplaceAll(mount, "*", "x")
	return body
}

func stringVal(scope, key string, val any) (string, error) {
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf(
			"schema: %s.%s: must be string, got %T", scope, key, val)
	}
	return s, nil
}

func stringSliceVal(scope, key string, val any) ([]string, error) {
	arr, ok := val.([]any)
	if !ok {
		return nil, fmt.Errorf(
			"schema: %s.%s: must be array of strings, got %T", scope, key, val)
	}
	out := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf(
				"schema: %s.%s[%d]: must be string, got %T", scope, key, i, item)
		}
		out = append(out, s)
	}
	return out, nil
}

func intVal(scope, key string, val any) (int, error) {
	switch n := val.(type) {
	case int64:
		return int(n), nil
	case int:
		return n, nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf(
				"schema: %s.%s: must be integer, got fractional %v", scope, key, val)
		}
		return int(n), nil
	}
	return 0, fmt.Errorf(
		"schema: %s.%s: must be integer, got %T", scope, key, val)
}

func isSupportedType(t Type) bool {
	switch t {
	case TypeString, TypeInteger, TypeFloat, TypeBoolean,
		TypeDatetime, TypeArray, TypeTable:
		return true
	}
	return false
}
