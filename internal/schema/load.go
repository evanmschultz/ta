package schema

import (
	"fmt"
	"io"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

// Meta-field keys recognised at the [<db>] root. Any other key at that
// level that is not a sub-table is a meta-schema violation.
const (
	metaFieldFile        = "file"
	metaFieldDirectory   = "directory"
	metaFieldCollection  = "collection"
	metaFieldFormat      = "format"
	metaFieldDescription = "description"
)

// Field-level keys recognised inside a [<db>.<type>.fields.<name>] table.
const (
	fieldKeyType        = "type"
	fieldKeyRequired    = "required"
	fieldKeyDescription = "description"
	fieldKeyEnum        = "enum"
	fieldKeyFormat      = "format"
	fieldKeyDefault     = "default"
)

// Type-level keys recognised on a [<db>.<type>] table (alongside the
// reserved `fields` sub-table).
const (
	typeKeyDescription = "description"
	typeKeyHeading     = "heading"
	typeKeyFields      = "fields"
)

// Load reads a schema config document from r and returns the resolved
// Registry. The top-level tables are databases; sub-tables under each db
// are record types; sub-tables under a record type's `fields` table are
// fields. See V2-PLAN §4.1.
//
// Load also enforces the meta-schema (§4.7): exactly one of file /
// directory / collection per db, valid format, valid heading on MD types,
// supported field types, etc.
func Load(r io.Reader) (Registry, error) {
	dec := toml.NewDecoder(r)

	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return Registry{}, fmt.Errorf("schema: parse config: %w", err)
	}
	return buildRegistry(raw)
}

// LoadBytes is the byte-slice convenience wrapper for Load. It is the
// entry point used by the meta-schema self-validator (the embedded
// literal never reaches a Reader).
func LoadBytes(buf []byte) (Registry, error) {
	var raw map[string]any
	if err := toml.Unmarshal(buf, &raw); err != nil {
		return Registry{}, fmt.Errorf("schema: parse config: %w", err)
	}
	return buildRegistry(raw)
}

func buildRegistry(raw map[string]any) (Registry, error) {
	reg := Registry{DBs: make(map[string]DB, len(raw))}

	// Sort db names so diagnostics are deterministic across runs.
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		bodyAny := raw[name]
		body, ok := bodyAny.(map[string]any)
		if !ok {
			return Registry{}, fmt.Errorf(
				"schema: %s: top-level entry must be a table, got %T", name, bodyAny)
		}
		db, err := buildDB(name, body)
		if err != nil {
			return Registry{}, err
		}
		reg.DBs[name] = db
	}

	// Meta-schema: no two dbs may point to the same path, and no db's
	// path may be a strict prefix of another's (§4.7).
	if err := checkPathUniqueness(reg); err != nil {
		return Registry{}, err
	}
	return reg, nil
}

func buildDB(name string, body map[string]any) (DB, error) {
	db := DB{Name: name, Types: map[string]SectionType{}}

	// Collect shape selectors and meta-fields.
	shapes := make([]Shape, 0, 3)
	for key, val := range body {
		switch key {
		case metaFieldFile:
			s, err := stringVal(name, key, val)
			if err != nil {
				return DB{}, err
			}
			shapes = append(shapes, ShapeFile)
			db.Path = s
		case metaFieldDirectory:
			s, err := stringVal(name, key, val)
			if err != nil {
				return DB{}, err
			}
			shapes = append(shapes, ShapeDirectory)
			db.Path = s
		case metaFieldCollection:
			s, err := stringVal(name, key, val)
			if err != nil {
				return DB{}, err
			}
			shapes = append(shapes, ShapeCollection)
			db.Path = s
		case metaFieldFormat:
			s, err := stringVal(name, key, val)
			if err != nil {
				return DB{}, err
			}
			if s != string(FormatTOML) && s != string(FormatMD) {
				return DB{}, fmt.Errorf(
					"schema: %s: format %q invalid (want %q or %q)",
					name, s, FormatTOML, FormatMD)
			}
			db.Format = Format(s)
		case metaFieldDescription:
			s, err := stringVal(name, key, val)
			if err != nil {
				return DB{}, err
			}
			db.Description = s
		default:
			// Must be a record-type sub-table.
			typeBody, ok := val.(map[string]any)
			if !ok {
				return DB{}, fmt.Errorf(
					"schema: %s.%s: unknown meta-field or non-table value (type %T); "+
						"record types must be tables, meta-fields must be one of "+
						"file/directory/collection/format/description",
					name, key, val)
			}
			st, err := buildType(name, key, typeBody)
			if err != nil {
				return DB{}, err
			}
			db.Types[key] = st
		}
	}

	// §4.7: exactly one of file / directory / collection.
	if len(shapes) == 0 {
		return DB{}, fmt.Errorf(
			"schema: %s: missing shape selector; set exactly one of %q, %q, %q",
			name, metaFieldFile, metaFieldDirectory, metaFieldCollection)
	}
	if len(shapes) > 1 {
		return DB{}, fmt.Errorf(
			"schema: %s: shape selectors are mutually exclusive; got %v",
			name, shapes)
	}
	db.Shape = shapes[0]

	if db.Format == "" {
		return DB{}, fmt.Errorf(
			"schema: %s: missing required meta-field %q (want %q or %q)",
			name, metaFieldFormat, FormatTOML, FormatMD)
	}

	// §4.7: file extension must match format.
	if db.Shape == ShapeFile {
		if err := checkFileExt(name, db.Path, db.Format); err != nil {
			return DB{}, err
		}
	}

	// §4.7: MD types require heading; duplicate heading levels rejected.
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

	// §4.7: every type has a description and at least one field.
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
	for key, val := range body {
		switch key {
		case fieldKeyType:
			s, err := stringVal(db+"."+typeName+".fields."+fname, key, val)
			if err != nil {
				return Field{}, err
			}
			f.Type = Type(s)
		case fieldKeyRequired:
			b, ok := val.(bool)
			if !ok {
				return Field{}, fmt.Errorf(
					"schema: %s.%s.fields.%s.required: must be boolean, got %T",
					db, typeName, fname, val)
			}
			f.Required = b
		case fieldKeyDescription:
			s, err := stringVal(db+"."+typeName+".fields."+fname, key, val)
			if err != nil {
				return Field{}, err
			}
			f.Description = s
		case fieldKeyEnum:
			arr, ok := val.([]any)
			if !ok {
				return Field{}, fmt.Errorf(
					"schema: %s.%s.fields.%s.enum: must be array, got %T",
					db, typeName, fname, val)
			}
			f.Enum = arr
		case fieldKeyFormat:
			s, err := stringVal(db+"."+typeName+".fields."+fname, key, val)
			if err != nil {
				return Field{}, err
			}
			f.Format = s
		case fieldKeyDefault:
			f.Default = val
		default:
			return Field{}, fmt.Errorf(
				"schema: %s.%s.fields.%s: unknown key %q (allowed: type, required, description, enum, format, default)",
				db, typeName, fname, key)
		}
	}
	if f.Type == "" {
		return Field{}, fmt.Errorf(
			"schema: %s.%s.fields.%s: missing required %q",
			db, typeName, fname, fieldKeyType)
	}
	if !isSupportedType(f.Type) {
		return Field{}, fmt.Errorf(
			"schema: %s.%s.fields.%s: unsupported type %q",
			db, typeName, fname, f.Type)
	}
	return f, nil
}

func checkFileExt(db, path string, format Format) error {
	var wantExt string
	switch format {
	case FormatTOML:
		wantExt = ".toml"
	case FormatMD:
		wantExt = ".md"
	default:
		return nil // unreachable; format already validated.
	}
	if len(path) < len(wantExt) || path[len(path)-len(wantExt):] != wantExt {
		return fmt.Errorf(
			"schema: %s: file %q extension does not match format %q (want %q)",
			db, path, format, wantExt)
	}
	return nil
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

func checkPathUniqueness(reg Registry) error {
	type entry struct {
		db   string
		path string
	}
	paths := make([]entry, 0, len(reg.DBs))
	for name, db := range reg.DBs {
		paths = append(paths, entry{db: name, path: db.Path})
	}
	// Exact collisions.
	seen := make(map[string]string, len(paths))
	for _, e := range paths {
		if prior, ok := seen[e.path]; ok {
			return fmt.Errorf(
				"schema: dbs %q and %q both target path %q", prior, e.db, e.path)
		}
		seen[e.path] = e.db
	}
	// Prefix nesting.
	for i := range paths {
		for j := range paths {
			if i == j {
				continue
			}
			if isPathPrefix(paths[i].path, paths[j].path) {
				return fmt.Errorf(
					"schema: db %q path %q is nested inside db %q path %q",
					paths[j].db, paths[j].path, paths[i].db, paths[i].path)
			}
		}
	}
	return nil
}

// isPathPrefix returns true when outer is a strict directory-prefix of
// inner. Path separator is "/" (schema paths are always POSIX-style
// relative paths per §4.1).
func isPathPrefix(outer, inner string) bool {
	if outer == "" || inner == "" || outer == inner {
		return false
	}
	if len(inner) <= len(outer) {
		return false
	}
	if inner[:len(outer)] != outer {
		return false
	}
	return inner[len(outer)] == '/'
}

func stringVal(scope, key string, val any) (string, error) {
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf(
			"schema: %s.%s: must be string, got %T", scope, key, val)
	}
	return s, nil
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
