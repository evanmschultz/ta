package schema

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Meta-field keys recognised at the [<db>] root. Any other key at that
// level that is not a sub-table is a meta-schema violation.
const (
	metaFieldPaths       = "paths"
	metaFieldFormat      = "format"
	metaFieldDescription = "description"
)

// Legacy meta-field keys removed in PLAN §12.17.9 Phase 9.1. Their presence
// in any schema file is a hard load-time error pointing at the migration
// note.
const (
	legacyMetaFieldFile       = "file"
	legacyMetaFieldDirectory  = "directory"
	legacyMetaFieldCollection = "collection"
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

// ErrLegacyShapeKey is returned when a schema file declares any of the
// removed `file` / `directory` / `collection` keys at the [<db>] root.
// Callers can match on this sentinel to surface migration guidance. See
// PLAN §12.17.9 Phase 9.1.
var ErrLegacyShapeKey = errors.New(
	"schema: legacy shape selector (file/directory/collection) " +
		"is no longer supported; use `paths = [...]` per PLAN §12.17.9")

// ErrOverlappingPaths is returned when two distinct dbs declare any
// overlapping entries in their `paths` slices. Two dbs sharing a mount
// would make addresses ambiguous. PLAN §12.17.9 enforces this at
// schema-load time.
//
// Phase 9.2 detection is glob-aware: each path is normalised
// (trailing-slash collection roots are treated as `<dir>/*` for the
// purpose of overlap, where `*` is one path segment), then converted to
// a regex with `*` → `[^/]+` plus a "or-anything-deeper" suffix when the
// mount is a collection root. Two paths overlap when there exists a
// concrete file path matched by both regexes; we cross-check by
// attempting to match each side's pattern against a representative
// expansion of the other, and additionally compare the static prefix.
var ErrOverlappingPaths = errors.New("schema: overlapping paths across dbs")

// Load reads a schema config document from r and returns the resolved
// Registry. The top-level tables are databases; sub-tables under each db
// are record types; sub-tables under a record type's `fields` table are
// fields. See V2-PLAN §4.1.
//
// Load also enforces the meta-schema (§4.7 + PLAN §12.17.9): every db
// declares `paths = [...]` (length 1+); old shape selectors are rejected;
// supported field types; etc.
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

	// Phase 9.1: cross-db paths-overlap rejection (PLAN §12.17.9).
	if err := checkPathsOverlap(reg); err != nil {
		return Registry{}, err
	}
	return reg, nil
}

func buildDB(name string, body map[string]any) (DB, error) {
	db := DB{Name: name, Types: map[string]SectionType{}}

	// Collect meta-fields and any record-type sub-tables.
	for key, val := range body {
		switch key {
		case legacyMetaFieldFile, legacyMetaFieldDirectory, legacyMetaFieldCollection:
			return DB{}, fmt.Errorf(
				"schema: %s.%s: %w", name, key, ErrLegacyShapeKey)
		case metaFieldPaths:
			paths, err := stringSliceVal(name, key, val)
			if err != nil {
				return DB{}, err
			}
			db.Paths = paths
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
						"paths/format/description (PLAN §12.17.9)",
					name, key, val)
			}
			st, err := buildType(name, key, typeBody)
			if err != nil {
				return DB{}, err
			}
			db.Types[key] = st
		}
	}

	// PLAN §12.17.9: paths is required and non-empty.
	if db.Paths == nil {
		return DB{}, fmt.Errorf(
			"schema: %s: missing required %q array (PLAN §12.17.9)",
			name, metaFieldPaths)
	}
	if len(db.Paths) == 0 {
		return DB{}, fmt.Errorf(
			"schema: %s: %q must declare at least one entry (PLAN §12.17.9)",
			name, metaFieldPaths)
	}
	for i, p := range db.Paths {
		if p == "" {
			return DB{}, fmt.Errorf(
				"schema: %s: %q[%d] is empty", name, metaFieldPaths, i)
		}
	}

	if db.Format == "" {
		return DB{}, fmt.Errorf(
			"schema: %s: missing required meta-field %q (want %q or %q)",
			name, metaFieldFormat, FormatTOML, FormatMD)
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

// checkPathsOverlap enforces the PLAN §12.17.9 cross-db invariant: no two
// dbs may share any entry in their `paths` slices. Two dbs with the same
// mount would make addresses ambiguous (`<file-relpath>.<id-tail>` cannot
// resolve to two distinct dbs).
//
// Phase 9.2 detects exact-string overlap and glob-aware overlap. Each
// mount is converted to a regex (`*` → `[^/]+`); collection roots
// (trailing `/`) match anything beneath them. Two mounts overlap when
// either regex matches a synthetic expansion of the other.
func checkPathsOverlap(reg Registry) error {
	type entry struct {
		db     string
		path   string
		format Format
	}
	flat := make([]entry, 0)
	for name, db := range reg.DBs {
		for _, p := range db.Paths {
			flat = append(flat, entry{db: name, path: p, format: db.Format})
		}
	}
	// Sort for deterministic error messages.
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
			if mountsOverlap(flat[i].path, flat[i].format, flat[j].path, flat[j].format) {
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
// shared concrete file. The check is symmetric: each mount's regex is
// matched against a synthetic expansion of the other, where `*` is
// expanded to a sentinel segment that satisfies `[^/]+`. Collection
// roots (trailing `/`) are treated as mounting any descendant file.
//
// PLAN §12.17.9 lock (2026-04-25): mount-string equality is the test,
// regardless of format. Two dbs cannot share a `paths` entry — the
// address grammar (`<file-relpath>.<type>.<id-tail>`, no db prefix)
// cannot disambiguate them at lookup time even when the on-disk
// extensions differ.
//
// Within a single format, the loader normalizes mount entries that
// ext-normalize to the same form so `paths = ["plans"]` and
// `paths = ["plans.toml"]` (both `format = "toml"`) still collide.
// Across formats, NO normalization is applied — the comparison is on
// the raw mount strings. So `["plans"]` (toml) and `["plans"]` (md)
// reject (mount strings literally equal); `["plans"]` (toml) and
// `["plans.md"]` (toml) accept (different bare strings, different
// files); `["docs/"]` (md) and `["docs.md"]` (md) accept (collection
// vs sibling file — physically distinct AND syntactically distinct
// mount strings).
func mountsOverlap(a string, fa Format, b string, fb Format) bool {
	aNorm := a
	bNorm := b
	if fa == fb {
		aNorm = normalizeMountForOverlap(a, fa)
		bNorm = normalizeMountForOverlap(b, fa)
	}
	if aNorm == bNorm {
		return true
	}
	reA := mountRegex(aNorm)
	reB := mountRegex(bNorm)
	if reA == nil || reB == nil {
		// Fall back to exact-string equality when either side cannot
		// be regex-encoded (defensive — every well-formed mount
		// should encode).
		return aNorm == bNorm
	}
	return reA.MatchString(mountSample(bNorm)) || reB.MatchString(mountSample(aNorm))
}

// normalizeMountForOverlap strips a trailing `.<format>` suffix from a
// non-collection mount so two declarations of the same file (one with
// the explicit extension, one without) compare equal. Collection
// mounts (trailing `/`) and the project-root collection (`.`) pass
// through unchanged — their mount strings name a directory, not a
// file basename, so extension stripping is meaningless.
//
// Only meaningful within a single format — `mountsOverlap` calls this
// only when both sides share the same `Format`. Cross-format
// comparisons use raw mount strings so `["plans"]` toml and
// `["plans"]` md collide on the bare equality check above.
func normalizeMountForOverlap(mount string, format Format) string {
	if format == "" {
		return mount
	}
	if mount == "." || strings.HasSuffix(mount, "/") {
		return mount
	}
	ext := "." + string(format)
	return strings.TrimSuffix(mount, ext)
}

// mountRegex compiles a mount entry into a regex anchored at both ends.
// `*` becomes `[^/]+` (one path segment, no dotfiles handled here —
// dotfile filtering lives in the resolver). A trailing `/` becomes a
// `/.+` suffix so a collection root matches descendants only — never
// the bare collection-root name itself. Per PLAN §12.17.9 lock
// (2026-04-25): `["docs/"]` is the directory-as-collection mount,
// disjoint from a sibling file `docs.md`; the regex must require at
// least one segment under the collection root or `mountSample("docs.md")
// = "docs.md"` would falsely match the optional-suffix form.
func mountRegex(mount string) *regexp.Regexp {
	collection := strings.HasSuffix(mount, "/")
	body := strings.TrimSuffix(mount, "/")
	parts := strings.Split(body, "/")
	for i, p := range parts {
		if p == "*" {
			parts[i] = `[^/]+`
		} else {
			parts[i] = regexp.QuoteMeta(p)
		}
	}
	pattern := "^" + strings.Join(parts, "/")
	if collection {
		pattern += `/.+$`
	} else {
		pattern += `$`
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

// mountSample returns a representative concrete-path expansion of mount
// suitable as input to another mount's regex. Globs expand to a literal
// sentinel segment ("x"); a trailing `/` expands by appending a
// sentinel filename so the dir-collection mount produces a file-shaped
// sample for the symmetric check. A non-collection mount with no `*`
// returns itself.
func mountSample(mount string) string {
	collection := strings.HasSuffix(mount, "/")
	body := strings.TrimSuffix(mount, "/")
	body = strings.ReplaceAll(body, "*", "x")
	if collection {
		body = strings.TrimSuffix(body, "/") + "/x"
	}
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
