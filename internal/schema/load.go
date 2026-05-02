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
const (
	metaFieldPaths       = "paths"
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

	if err := checkPathsOverlap(reg); err != nil {
		return Registry{}, err
	}
	return reg, nil
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
						"paths/description (PLAN §12.17.9 F10)",
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
