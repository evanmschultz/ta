package schema

import (
	"errors"
	"fmt"
	"io"
	"maps"
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
//
// Per F22: `bases` is reserved at the db level for the named-base
// table ([<db>.bases.<name>]); it is therefore not a valid record
// type name. Bases hold reusable field bundles concrete types and
// other bases inherit via `extends`.
const (
	metaFieldPaths       = "paths"
	metaFieldDescription = "description"
	metaFieldTypes       = "types"
	metaFieldBases       = "bases"
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
	fieldKeyFields        = "fields"
)

// Type-level keys recognised on a [<db>.<type>] table (alongside the
// reserved `fields` sub-table).
//
// Per F22: `extends` names a base ([<db>.bases.<name>]) whose fields
// are merged into the concrete type at load time. The keyword is
// discarded after flattening. An explicit empty string (`extends = ""`)
// is treated identically to omitting the key — neither path triggers
// inheritance.
//
// Per F23: `auto_spawn` declares a `[<db>.<type>.auto_spawn]` sub-table
// with an `on_create = [...]` array of spawn specs that fire after a
// successful Create of a record of this type.
const (
	typeKeyDescription = "description"
	typeKeyHeading     = "heading"
	typeKeyFields      = "fields"
	typeKeyExtends     = "extends"
	typeKeyAutoSpawn   = "auto_spawn"
	// Per F31: record_per declares per-record granularity for MD
	// types ("section" — default — or "file" — file-as-record).
	// body_field is the field that receives the markdown body on
	// file-as-record types.
	typeKeyRecordPer = "record_per"
	typeKeyBodyField = "body_field"
)

// auto_spawn sub-table keys.
const (
	autoSpawnKeyOnCreate   = "on_create"
	spawnSpecKeyType       = "type"
	spawnSpecKeyIDTemplate = "id_template"
	spawnSpecKeyFields     = "fields"
	spawnTokenParentID     = "{parent_id}"
	spawnTokenIndex        = "{index}"
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

	// ErrUnknownBase is returned when a type or base body declares
	// `extends = "X"` and `X` is not a registered base under any
	// [<db>.bases.<name>]. Bases share a Registry-wide namespace.
	ErrUnknownBase = errors.New("schema: unknown base referenced via extends")

	// ErrExtendsCycle is returned when extends-resolution detects a
	// cycle (self-reference or mutual: A → B → A). The error message
	// includes the full chain for debugging.
	ErrExtendsCycle = errors.New("schema: extends chain cycle")

	// ErrExtendsTargetNotBase is returned when `extends` names a
	// concrete record type rather than a base. Bases are the only
	// legal target for inheritance per F22.
	ErrExtendsTargetNotBase = errors.New("schema: extends target is not a base")

	// ErrEmptyBase is returned when a [<db>.bases.<name>] body
	// declares neither `extends` nor any fields. Such a base has no
	// useful semantics.
	ErrEmptyBase = errors.New("schema: base must declare at least one field or extends")

	// ErrBaseAliasNameCollision is returned when a single name appears
	// simultaneously as a base ([<db>.bases.<name>]) and as a type
	// alias ([<db>.types.<name>]) anywhere in the Registry. Bases and
	// aliases share separate namespaces but reusing a name across them
	// is rejected to keep diagnostics unambiguous.
	ErrBaseAliasNameCollision = errors.New("schema: name declared as both a base and a type alias")

	// ErrAliasExtendsNotAllowed is returned when an alias body
	// ([<db>.types.<alias>]) carries an `extends` key. Aliases compose
	// via element_type chains, not extends; the two mechanisms do not
	// cross.
	ErrAliasExtendsNotAllowed = errors.New("schema: alias cannot use extends; use bases for inheritance")

	// ErrBaseNameCollision is returned when a declared base name
	// collides with any other declared symbol anywhere in the
	// Registry — an alias ([<db>.types.<name>]), another base
	// ([<db>.bases.<name>]), or a concrete record type
	// ([<db>.<name>]). Bases are Registry-wide global symbols; a base
	// name must be unique across every namespace to keep extends
	// resolution unambiguous (`extends = "X"` must always resolve to
	// exactly one declaration). Pure base-vs-base duplicates are
	// caught earlier with a more specific message in collectBases;
	// pure base-vs-alias collisions surface as ErrBaseAliasNameCollision
	// for back-compat with F21 phrasing.
	ErrBaseNameCollision = errors.New("schema: base name collides with another declared symbol")

	// ErrSpawnCycle is returned when the spawn graph (edges from each
	// type T to every target type listed in T.AutoSpawn) contains a
	// cycle, e.g. T spawns itself directly or transitively. The wrapped
	// message names the chain. Per F23.
	ErrSpawnCycle = errors.New("schema: auto_spawn cycle")

	// ErrSpawnUnknownType is returned when a spawn spec's `type` does
	// not resolve to a concrete record type. Bases and aliases are
	// rejected; the spawn target must be a real record-type body. Per
	// F23.
	ErrSpawnUnknownType = errors.New("schema: auto_spawn target type unknown or not concrete")

	// ErrSpawnInvalidIDTemplate is returned when a spawn spec's
	// `id_template` is empty, contains an unsupported interpolation
	// token (anything other than `{parent_id}` or `{index}`), or has a
	// malformed `{...}` literal. Per F23.
	ErrSpawnInvalidIDTemplate = errors.New("schema: auto_spawn id_template invalid")

	// ErrSpawnIncompletePayload is returned when a spawn spec's
	// statically-declared `fields` payload omits a required field on
	// the target type that has no default. Detected at load (static
	// shape check) and at runtime (final post-interpolation Validate
	// against the target type). Per F23.
	ErrSpawnIncompletePayload = errors.New("schema: auto_spawn fields payload incomplete")

	// ErrFileRecordWithHeading is returned when a type declares both
	// `record_per = "file"` and `heading = N`. Headings are the
	// per-section anchor for section-mode dbs and meaningless for
	// file-as-record types — co-declaring them is a loud failure per
	// F31 contract.
	ErrFileRecordWithHeading = errors.New(
		"schema: heading is forbidden on file-as-record types (record_per = \"file\")")

	// ErrFileRecordMissingBodyField is returned when a type declares
	// `record_per = "file"` but omits `body_field`. The body field is
	// the type-level pointer to the field that receives the markdown
	// body; without it the backend has nowhere to put body bytes.
	// Per F31.
	ErrFileRecordMissingBodyField = errors.New(
		"schema: file-as-record types require body_field = \"<field-name>\"")

	// ErrBodyFieldUnknown is returned when `body_field = "<name>"`
	// names a field that does not appear in the type's resolved
	// Fields map. Per F31.
	ErrBodyFieldUnknown = errors.New(
		"schema: body_field references a field not declared on the type")

	// ErrBodyFieldOnSectionType is returned when a section-mode type
	// (record_per != "file") declares `body_field`. The body field is
	// only meaningful on file-as-record types; declaring it elsewhere
	// is a contract violation per F31's loud-error invariant.
	ErrBodyFieldOnSectionType = errors.New(
		"schema: body_field is only valid on file-as-record types (record_per = \"file\")")

	// ErrRecordPerInvalid is returned when `record_per` carries a
	// value other than "section" or "file". Per F31.
	ErrRecordPerInvalid = errors.New(
		"schema: record_per must be \"section\" or \"file\"")

	// ErrRecordPerOnTOML is returned when a TOML db's type declares
	// `record_per`. Only MD dbs support per-record granularity;
	// declaring it on TOML is a meaningless co-declaration. Per F31.
	ErrRecordPerOnTOML = errors.New(
		"schema: record_per is only valid on MD-format dbs")

	// ErrMixedRecordModes is returned when one db declares both
	// section-mode and file-as-record types. Per F31's locked design
	// rule, a single db must be all-file-mode OR all-section-mode —
	// not a mix. Mixing prevents reliable id-grammar disambiguation
	// at the address-resolver layer.
	ErrMixedRecordModes = errors.New(
		"schema: db cannot mix section-mode and file-as-record types")
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

// extendsRecord is the per-type extends sidecar populated during Phase
// A.5. The `extends` keyword is discarded from the resolved Registry
// after Phase B.0 expandBases runs, so the record carries everything
// needed to flatten a type's inherited fields without polluting
// SectionType. Keyed by "<db>.<type>" because type names are only
// unique within a db.
type extendsRecord struct {
	db      string
	typ     string
	base    string
	hasBase bool
}

func buildRegistry(raw map[string]any) (Registry, error) {
	reg := Registry{DBs: make(map[string]DB, len(raw))}

	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)

	// Phase A.0 — collect base declarations. Each [<db>.bases.<name>]
	// block is a reusable field-bundle body (description + optional
	// extends + optional fields map) stashed in a Registry-wide base
	// table. Bases retain raw `extends` strings at this stage; chains
	// are resolved transitively by resolveBase during phase B.0.
	baseRaw := map[string]*baseDecl{}

	for _, name := range names {
		bodyAny := raw[name]
		body, ok := bodyAny.(map[string]any)
		if !ok {
			return Registry{}, fmt.Errorf(
				"schema: %s: top-level entry must be a table, got %T", name, bodyAny)
		}
		if err := collectBases(name, body, baseRaw); err != nil {
			return Registry{}, err
		}
	}

	// Phase A — collect alias declarations. Each [<db>.types.<alias>]
	// block is a record-type-shaped body (description + fields map)
	// stashed in a Registry-wide alias table. Aliases at this stage
	// retain raw element_type strings; alias-to-alias chains and
	// alias-references inside alias bodies are resolved transitively
	// by resolveAlias during phase B.
	aliasRaw := map[string]map[string]Field{}

	for _, name := range names {
		body := raw[name].(map[string]any)
		if err := collectAliases(name, body, aliasRaw); err != nil {
			return Registry{}, err
		}
	}

	// Cross-namespace name collision: a name may be a base or an
	// alias, never both. Catch as early as possible to keep error
	// messages unambiguous.
	if err := checkBaseAliasCollision(baseRaw, aliasRaw); err != nil {
		return Registry{}, err
	}

	// Phase A.5 — build dbs / types / fields. element_type / element_fields
	// are recorded as-declared; alias names appear verbatim in ElementType
	// and are inlined in phase B. The `extends` key on each type body is
	// stashed in an extends-sidecar so phase B.0 can flatten base fields
	// in without storing the keyword on SectionType.
	extendsBy := []extendsRecord{}
	for _, name := range names {
		body := raw[name].(map[string]any)
		db, recs, err := buildDB(name, body)
		if err != nil {
			return Registry{}, err
		}
		reg.DBs[name] = db
		extendsBy = append(extendsBy, recs...)
	}

	// Cross-namespace collision check: a base name must not match any
	// concrete record type in any db. The base-vs-alias case is already
	// covered by checkBaseAliasCollision above; the base-vs-base case
	// is caught in collectBases. This check closes the remaining gaps
	// (base-vs-concrete-type, same-db and different-db) so a bare
	// `extends = "X"` always resolves unambiguously to one symbol.
	if err := checkBaseNameCollisions(baseRaw, reg); err != nil {
		return Registry{}, err
	}

	// Phase B.0 — expand base references on concrete types. Bases must
	// flatten before alias expansion: a base's field can declare an
	// element_type that names an alias, and inlining the alias requires
	// the base's fields to already be present on the inheriting type.
	if err := expandBases(reg, baseRaw, extendsBy); err != nil {
		return Registry{}, err
	}

	// Phase B.0.5 — propagate auto_spawn through extends (F23). When a
	// base declares an auto_spawn block, every inheriting concrete type
	// without its own auto_spawn picks up the base's specs. Same
	// wholesale-replace rule as fields: a concrete type's own
	// auto_spawn wins. Must run BEFORE cycle / completeness validation
	// so base-declared specs participate as edges in the type graph.
	if err := expandAutoSpawn(reg, baseRaw, extendsBy); err != nil {
		return Registry{}, err
	}

	// Phase B.0.6 — validate the spawn graph for cycles (F23). Build
	// edges T1 → T2 for each spec on T1 targeting T2; DFS catches self-
	// references and longer chains.
	if err := checkSpawnCycles(reg); err != nil {
		return Registry{}, err
	}

	// Phase B.0.7 — validate spawn-spec completeness (F23). Every spec
	// must target a concrete record type, carry a valid id_template,
	// and supply enough static fields to satisfy the target type's
	// required-field set (or rely on per-target defaults). Runs after
	// Phase B.0.5 so propagated specs are checked, and after Phase B.0
	// so target types' Fields maps include any inherited base fields.
	if err := checkSpawnSpecs(reg); err != nil {
		return Registry{}, err
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

// baseDecl is the parsed form of a [<db>.bases.<name>] body. The raw
// `extends` string (empty when absent) is retained so phase B.0
// resolves chains in topological order.
//
// Per F23, a base may also carry an `auto_spawn` sub-table; the specs
// propagate onto inheriting concrete types via the same wholesale-
// replace rule as `fields` (concrete type's own auto_spawn wins). The
// spec slice is parsed verbatim here; cycle / completeness validation
// runs after full Registry assembly.
type baseDecl struct {
	dbName    string
	name      string
	extends   string
	fields    map[string]Field
	autoSpawn []SpawnSpec
}

// checkBaseAliasCollision rejects any name that appears as both a base
// and an alias anywhere in the Registry. Bases and aliases live in
// separate namespaces but reusing a name across them defeats the
// purpose of having two namespaces and would make diagnostics
// ambiguous ("did you mean the base X or the alias X?").
func checkBaseAliasCollision(
	baseRaw map[string]*baseDecl,
	aliasRaw map[string]map[string]Field,
) error {
	names := make([]string, 0, len(baseRaw))
	for n := range baseRaw {
		if _, dup := aliasRaw[n]; dup {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return fmt.Errorf(
		"%w: %q",
		ErrBaseAliasNameCollision, names[0])
}

// checkBaseNameCollisions rejects any base name that also appears as
// a concrete record type ([<db>.<name>]) in any db of reg. The
// alias-vs-base case is handled by checkBaseAliasCollision (a more
// specific message tied to ErrBaseAliasNameCollision); the
// base-vs-base case is handled inside collectBases. The remaining
// gap — bases sharing a name with a concrete type — is closed here.
//
// Bases live in a Registry-wide namespace and are referenced by bare
// name from `extends`, so a base named `Task` collisional with any
// db's concrete `[<db>.task]` would force callers (and reviewers) to
// guess which symbol won. Reject up front.
//
// Output is sorted (lexicographically by colliding name then db) so
// repeated loads surface the same offender in the diagnostic.
func checkBaseNameCollisions(baseRaw map[string]*baseDecl, reg Registry) error {
	type hit struct {
		name string
		db   string
	}
	var hits []hit
	baseNames := make([]string, 0, len(baseRaw))
	for n := range baseRaw {
		baseNames = append(baseNames, n)
	}
	sort.Strings(baseNames)
	dbNames := make([]string, 0, len(reg.DBs))
	for n := range reg.DBs {
		dbNames = append(dbNames, n)
	}
	sort.Strings(dbNames)
	for _, name := range baseNames {
		for _, dbName := range dbNames {
			if _, ok := reg.DBs[dbName].Types[name]; ok {
				hits = append(hits, hit{name: name, db: dbName})
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	first := hits[0]
	return fmt.Errorf(
		"%w: %q (also a concrete record type at [%s.%s])",
		ErrBaseNameCollision, first.name, first.db, first.name)
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
//
// Per F22, `extends` on an alias body is rejected outright — aliases
// compose via element_type chains and bases compose via extends; the
// two mechanisms do not cross.
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
		case typeKeyExtends:
			return nil, fmt.Errorf(
				"%w: alias %q (declared at %s)",
				ErrAliasExtendsNotAllowed, alias, scope)
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

// collectBases walks one db body for its [<db>.bases.<name>] entries,
// building each base body's `fields.<name>` table into a map of Field
// values plus an optional `extends` chain link. Bases share a flat
// Registry-wide namespace so duplicates across dbs are rejected. The
// bare `description = "..."` key on [<db>.bases] (documentation only)
// is permitted and ignored.
func collectBases(dbName string, body map[string]any, dst map[string]*baseDecl) error {
	bBody, ok := body[metaFieldBases]
	if !ok {
		return nil
	}
	basesBody, ok := bBody.(map[string]any)
	if !ok {
		return fmt.Errorf(
			"schema: %s.%s: must be a table of named base declarations, got %T",
			dbName, metaFieldBases, bBody)
	}
	baseNames := make([]string, 0, len(basesBody))
	for n := range basesBody {
		baseNames = append(baseNames, n)
	}
	sort.Strings(baseNames)
	for _, base := range baseNames {
		val := basesBody[base]
		if base == typeKeyDescription {
			// Allow a bare description string at [<db>.bases] for
			// documentation; not a base.
			if _, isStr := val.(string); !isStr {
				return fmt.Errorf(
					"schema: %s.%s.description: must be string, got %T",
					dbName, metaFieldBases, val)
			}
			continue
		}
		bb, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf(
				"schema: %s.%s.%s: base body must be a table, got %T",
				dbName, metaFieldBases, base, val)
		}
		if _, dup := dst[base]; dup {
			return fmt.Errorf(
				"schema: %s.%s.%s: base %q already declared (bases share a Registry-wide namespace)",
				dbName, metaFieldBases, base, base)
		}
		decl, err := buildBaseDecl(dbName, base, bb)
		if err != nil {
			return err
		}
		dst[base] = decl
	}
	return nil
}

// buildBaseDecl parses one [<db>.bases.<name>] body. The body shape:
// description (optional), extends (optional, names another base),
// fields map (entries built via buildField), and an optional
// auto_spawn sub-table (per F23) that propagates onto inheriting
// concrete types. A base body MUST declare at least one field of its
// own OR carry an `extends` link.
func buildBaseDecl(dbName, base string, body map[string]any) (*baseDecl, error) {
	scope := dbName + "." + metaFieldBases + "." + base
	decl := &baseDecl{dbName: dbName, name: base, fields: map[string]Field{}}
	var fieldsBody map[string]any
	for key, val := range body {
		switch key {
		case typeKeyDescription:
			if _, ok := val.(string); !ok {
				return nil, fmt.Errorf(
					"schema: %s.description: must be string, got %T", scope, val)
			}
		case typeKeyExtends:
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf(
					"schema: %s.extends: must be string, got %T", scope, val)
			}
			decl.extends = s
		case typeKeyFields:
			fb, ok := val.(map[string]any)
			if !ok {
				return nil, fmt.Errorf(
					"schema: %s.fields: must be a table, got %T", scope, val)
			}
			fieldsBody = fb
		case typeKeyAutoSpawn:
			specs, err := buildAutoSpawn(dbName, metaFieldBases+"."+base, val)
			if err != nil {
				return nil, err
			}
			decl.autoSpawn = specs
		default:
			return nil, fmt.Errorf(
				"schema: %s: unknown key %q (allowed: description, extends, fields, auto_spawn)",
				scope, key)
		}
	}
	if len(fieldsBody) == 0 && decl.extends == "" {
		return nil, fmt.Errorf("%w: base %q at %s", ErrEmptyBase, base, scope)
	}
	for fname, fval := range fieldsBody {
		fbody, ok := fval.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"schema: %s.fields.%s: must be a table, got %T",
				scope, fname, fval)
		}
		f, err := buildField(dbName, metaFieldBases+"."+base, fname, fbody)
		if err != nil {
			return nil, err
		}
		decl.fields[fname] = f
	}
	return decl, nil
}

// expandBases walks every type carrying an `extends` link and merges
// the named base's resolved fields into the type's Fields map. Same-
// named child fields wholesale replace the base's declaration. Base-
// to-base chains resolve transitively via resolveBase; cycles (self-
// reference or A → B → A) surface as ErrExtendsCycle.
//
// The `extends` keyword is consumed entirely here; nothing about it
// survives into the resolved Registry. Per F22, the `extends` target
// must be a base — concrete record types cannot be extended. The
// extendsBy slice records each (db, type, base) triple from Phase
// A.5; concrete-type names are checked against the per-db Types map
// to surface a clear ErrExtendsTargetNotBase when the name resolves
// to a non-base.
func expandBases(reg Registry, baseRaw map[string]*baseDecl, extendsBy []extendsRecord) error {
	resolved := map[string]map[string]Field{}
	baseNames := make([]string, 0, len(baseRaw))
	for n := range baseRaw {
		baseNames = append(baseNames, n)
	}
	sort.Strings(baseNames)
	for _, name := range baseNames {
		visiting := map[string]bool{}
		if _, err := resolveBase(name, baseRaw, resolved, visiting, nil); err != nil {
			return err
		}
	}

	for _, rec := range extendsBy {
		if !rec.hasBase {
			continue
		}
		baseFields, ok := resolved[rec.base]
		if !ok {
			// Differentiate "name unknown to bases" from "name belongs
			// to a concrete type". The latter surfaces the bases-only
			// rule explicitly; the former is a plain unknown reference.
			if _, isType := registryHasType(reg, rec.base); isType {
				return fmt.Errorf(
					"%w: %s.%s extends %q (a concrete record type, not a base)",
					ErrExtendsTargetNotBase, rec.db, rec.typ, rec.base)
			}
			return fmt.Errorf(
				"%w: %s.%s extends %q (not declared as a base)",
				ErrUnknownBase, rec.db, rec.typ, rec.base)
		}

		db := reg.DBs[rec.db]
		st := db.Types[rec.typ]
		merged := make(map[string]Field, len(baseFields)+len(st.Fields))
		// Base fields first (deep-cloned so child mutations cannot
		// affect the cached base copy across multiple inheritors).
		for k, v := range baseFields {
			merged[k] = cloneField(v)
		}
		// Child fields wholesale-replace any base entries with the
		// same key; child key wins regardless of map iteration order.
		maps.Copy(merged, st.Fields)
		st.Fields = merged
		db.Types[rec.typ] = st
		reg.DBs[rec.db] = db
	}
	return nil
}

// registryHasType reports whether `name` is a concrete record type in
// any db of reg. Used by expandBases to differentiate ErrUnknownBase
// (name nowhere declared) from ErrExtendsTargetNotBase (name is a
// type, not a base).
func registryHasType(reg Registry, name string) (string, bool) {
	for dbName, db := range reg.DBs {
		if _, ok := db.Types[name]; ok {
			return dbName, true
		}
	}
	return "", false
}

// expandAutoSpawn propagates auto_spawn specs from bases onto
// inheriting concrete types (F23). The rule mirrors the same-named-
// field rule in expandBases: a concrete type's own auto_spawn block
// wholesale-replaces an inherited one; only when the type has no
// auto_spawn of its own does the base contribution apply. Multi-level
// chains resolve via resolveBaseAutoSpawn — the deepest concrete-
// override along the chain wins (which for bases means the closest
// base to the concrete type that declares auto_spawn, since the
// concrete type itself is checked separately above).
func expandAutoSpawn(reg Registry, baseRaw map[string]*baseDecl, extendsBy []extendsRecord) error {
	resolved := map[string][]SpawnSpec{}
	baseNames := make([]string, 0, len(baseRaw))
	for n := range baseRaw {
		baseNames = append(baseNames, n)
	}
	sort.Strings(baseNames)
	for _, name := range baseNames {
		visiting := map[string]bool{}
		if _, err := resolveBaseAutoSpawn(name, baseRaw, resolved, visiting); err != nil {
			return err
		}
	}
	for _, rec := range extendsBy {
		if !rec.hasBase {
			continue
		}
		db := reg.DBs[rec.db]
		st := db.Types[rec.typ]
		// Concrete type's own auto_spawn (set during buildType) wins
		// wholesale; nothing to propagate.
		if len(st.AutoSpawn) > 0 {
			continue
		}
		baseSpecs, ok := resolved[rec.base]
		if !ok || len(baseSpecs) == 0 {
			continue
		}
		// Deep-clone so subsequent mutations on one inheritor cannot
		// pollute another that shares the same base.
		st.AutoSpawn = cloneSpawnSpecs(baseSpecs)
		db.Types[rec.typ] = st
		reg.DBs[rec.db] = db
	}
	return nil
}

// resolveBaseAutoSpawn returns the auto_spawn specs that apply to the
// base named `name`, walking its extends chain. The closest base that
// declares an auto_spawn block wins; chained bases without their own
// auto_spawn fall through to their parent. Returns nil when no base in
// the chain declares auto_spawn.
//
// Cycle detection is intentionally absent here — Phase B.0 expandBases
// already runs full resolveBase on every base, and any extends-chain
// cycle surfaces as ErrExtendsCycle there. By the time this function
// runs, every chain is known to be acyclic.
func resolveBaseAutoSpawn(
	name string,
	raw map[string]*baseDecl,
	resolved map[string][]SpawnSpec,
	visiting map[string]bool,
) ([]SpawnSpec, error) {
	if out, done := resolved[name]; done {
		return out, nil
	}
	decl, ok := raw[name]
	if !ok {
		// Should not happen — extends targets are validated by
		// expandBases before this phase runs.
		return nil, nil
	}
	if visiting[name] {
		// Defensive: cycles caught earlier; bail out cleanly.
		return nil, nil
	}
	visiting[name] = true
	defer delete(visiting, name)

	if len(decl.autoSpawn) > 0 {
		out := cloneSpawnSpecs(decl.autoSpawn)
		resolved[name] = out
		return out, nil
	}
	if decl.extends != "" {
		parent, err := resolveBaseAutoSpawn(decl.extends, raw, resolved, visiting)
		if err != nil {
			return nil, err
		}
		resolved[name] = parent
		return parent, nil
	}
	resolved[name] = nil
	return nil, nil
}

// cloneSpawnSpecs deep-copies a SpawnSpec slice including each spec's
// Fields map. Used so propagated specs cannot leak mutations across
// inheritors.
func cloneSpawnSpecs(in []SpawnSpec) []SpawnSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]SpawnSpec, len(in))
	for i, s := range in {
		out[i] = SpawnSpec{Type: s.Type, IDTemplate: s.IDTemplate}
		if len(s.Fields) > 0 {
			out[i].Fields = make(map[string]any, len(s.Fields))
			maps.Copy(out[i].Fields, s.Fields)
		}
	}
	return out
}

// checkSpawnCycles runs DFS over the spawn-graph of concrete record
// types: each type T is a node; for each spec in T.AutoSpawn there is
// an edge T → spec.Type. A cycle in that graph (self-loop or longer)
// surfaces as ErrSpawnCycle with the discovered chain. Per F23.
//
// Unknown spec.Type values are tolerated here so the dedicated
// ErrSpawnUnknownType message in checkSpawnSpecs can fire on the same
// load — they are simply skipped as edges.
func checkSpawnCycles(reg Registry) error {
	idOf := func(db, typ string) string { return db + "." + typ }

	// Snapshot every concrete type's outgoing edges in a stable
	// (db, typ) order so cycle messages are deterministic.
	edges := map[string][]string{}
	dbNames := make([]string, 0, len(reg.DBs))
	for n := range reg.DBs {
		dbNames = append(dbNames, n)
	}
	sort.Strings(dbNames)
	var roots []string
	for _, dbName := range dbNames {
		dbDecl := reg.DBs[dbName]
		typeNames := make([]string, 0, len(dbDecl.Types))
		for n := range dbDecl.Types {
			typeNames = append(typeNames, n)
		}
		sort.Strings(typeNames)
		for _, tName := range typeNames {
			st := dbDecl.Types[tName]
			from := idOf(dbName, tName)
			roots = append(roots, from)
			for _, spec := range st.AutoSpawn {
				// spec.Type is "<db>.<type>"; only treat it as an edge
				// when it resolves to a known concrete type.
				targetDB, targetType, rest := splitFirstTwo(spec.Type)
				if targetDB == "" || targetType == "" || rest != "" {
					continue
				}
				targetDecl, ok := reg.DBs[targetDB]
				if !ok {
					continue
				}
				if _, ok := targetDecl.Types[targetType]; !ok {
					continue
				}
				edges[from] = append(edges[from], idOf(targetDB, targetType))
			}
		}
	}

	const (
		white = 0 // unseen
		gray  = 1 // on stack
		black = 2 // finished
	)
	color := map[string]int{}
	var stack []string
	var dfs func(at string) error
	dfs = func(at string) error {
		color[at] = gray
		stack = append(stack, at)
		for _, next := range edges[at] {
			switch color[next] {
			case white:
				if err := dfs(next); err != nil {
					return err
				}
			case gray:
				// Cycle. Slice the stack from the first occurrence of
				// `next` to the end and append next again to render the
				// loop closure.
				start := 0
				for i, n := range stack {
					if n == next {
						start = i
						break
					}
				}
				cycle := append([]string(nil), stack[start:]...)
				cycle = append(cycle, next)
				return fmt.Errorf("%w: %s", ErrSpawnCycle, strings.Join(cycle, " → "))
			}
		}
		color[at] = black
		stack = stack[:len(stack)-1]
		return nil
	}
	for _, r := range roots {
		if color[r] == white {
			if err := dfs(r); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSpawnSpecs runs the per-spec completeness checks (F23 Phase
// B.0.7). For every spec on every concrete type:
//
//   - spec.Type must resolve to a concrete record type in the
//     registry. Bases / aliases / unknowns surface as
//     ErrSpawnUnknownType.
//   - spec.IDTemplate must contain only the supported `{parent_id}`
//     and `{index}` tokens. Unknown / malformed tokens surface as
//     ErrSpawnInvalidIDTemplate.
//   - The static `fields` table plus the target type's defaulting
//     layer must cover every required field on the target type.
//     Missing required fields surface as ErrSpawnIncompletePayload.
func checkSpawnSpecs(reg Registry) error {
	dbNames := make([]string, 0, len(reg.DBs))
	for n := range reg.DBs {
		dbNames = append(dbNames, n)
	}
	sort.Strings(dbNames)
	for _, dbName := range dbNames {
		dbDecl := reg.DBs[dbName]
		typeNames := make([]string, 0, len(dbDecl.Types))
		for n := range dbDecl.Types {
			typeNames = append(typeNames, n)
		}
		sort.Strings(typeNames)
		for _, tName := range typeNames {
			st := dbDecl.Types[tName]
			origin := dbName + "." + tName
			for i, spec := range st.AutoSpawn {
				if err := checkOneSpawnSpec(reg, origin, i, spec); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkOneSpawnSpec validates one spec. `origin` is the dotted address
// of the type that owns the spec (used in error messages).
func checkOneSpawnSpec(reg Registry, origin string, idx int, spec SpawnSpec) error {
	specScope := fmt.Sprintf("%s.auto_spawn[%d]", origin, idx)

	// Target-type resolution.
	targetDB, targetType, rest := splitFirstTwo(spec.Type)
	if targetDB == "" || targetType == "" || rest != "" {
		return fmt.Errorf(
			"%w: %s: type %q must be db-qualified `<db>.<type>`",
			ErrSpawnUnknownType, specScope, spec.Type)
	}
	dbDecl, ok := reg.DBs[targetDB]
	if !ok {
		return fmt.Errorf(
			"%w: %s: db %q not registered (target type %q)",
			ErrSpawnUnknownType, specScope, targetDB, spec.Type)
	}
	targetSt, ok := dbDecl.Types[targetType]
	if !ok {
		return fmt.Errorf(
			"%w: %s: type %q not declared on db %q",
			ErrSpawnUnknownType, specScope, targetType, targetDB)
	}

	// id_template token validation.
	if err := validateSpawnTemplateTokens(spec.IDTemplate); err != nil {
		return fmt.Errorf(
			"%w: %s: %v", ErrSpawnInvalidIDTemplate, specScope, err)
	}

	// Required-field coverage. A required field on the target type
	// without a default must appear in spec.Fields. Defaults satisfy
	// the requirement at the schema layer; ops.Create still validates
	// post-merge against the registry.
	for fname, f := range targetSt.Fields {
		if !f.Required {
			continue
		}
		if f.Default != nil {
			continue
		}
		if _, present := spec.Fields[fname]; present {
			continue
		}
		return fmt.Errorf(
			"%w: %s: target type %q requires field %q (no default) but spec.fields omits it",
			ErrSpawnIncompletePayload, specScope, spec.Type, fname)
	}
	return nil
}

// validateSpawnTemplateTokens scans s for `{...}` tokens and rejects
// any token whose name is not `parent_id` or `index`. An unbalanced
// `{` returns an error too. The template MUST contain `{parent_id}`
// so each spawn produces a parent-unique id; without it, the template
// expands to the same string for every parent and second-create lands
// `ErrRecordExists` mid-spawn (partial-write hazard). Per F23 v1:
// literal-brace escaping is not supported.
func validateSpawnTemplateTokens(s string) error {
	if s == "" {
		return fmt.Errorf("id_template is empty")
	}
	hasParentID := false
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			return fmt.Errorf("unterminated %q at offset %d", "{", i)
		}
		token := s[i : i+end+1]
		switch token {
		case spawnTokenParentID:
			hasParentID = true
			i += end
			continue
		case spawnTokenIndex:
			i += end
			continue
		default:
			return fmt.Errorf("unknown token %q (allowed: %s, %s)",
				token, spawnTokenParentID, spawnTokenIndex)
		}
	}
	if !hasParentID {
		return fmt.Errorf("id_template must contain %s for parent-uniqueness", spawnTokenParentID)
	}
	return nil
}

// resolveBase returns the fully-flattened field map for the base named
// `name`. Base-to-base chains resolve transitively. `visiting` tracks
// names on the current recursion stack for cycle detection; `chain`
// records the human-readable path used in cycle messages.
func resolveBase(
	name string,
	raw map[string]*baseDecl,
	resolved map[string]map[string]Field,
	visiting map[string]bool,
	chain []string,
) (map[string]Field, error) {
	if out, done := resolved[name]; done {
		return out, nil
	}
	decl, ok := raw[name]
	if !ok {
		return nil, fmt.Errorf(
			"%w: base %q referenced but not declared", ErrUnknownBase, name)
	}
	if visiting[name] {
		return nil, fmt.Errorf(
			"%w: %s → %s",
			ErrExtendsCycle, strings.Join(append(chain, name), " → "), name)
	}
	visiting[name] = true
	defer delete(visiting, name)
	chain = append(chain, name)

	out := map[string]Field{}
	if decl.extends != "" {
		parent, err := resolveBase(decl.extends, raw, resolved, visiting, chain)
		if err != nil {
			return nil, err
		}
		for k, v := range parent {
			out[k] = cloneField(v)
		}
	}
	// Own fields wholesale-replace inherited entries with the same
	// key; child key wins regardless of map iteration order.
	maps.Copy(out, decl.fields)
	resolved[name] = out
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
	// Direct nested-table inner shape (F28). Sub-fields can themselves
	// declare `element_type = "<alias>"` which must resolve through the
	// same alias-inlining path used for top-level fields.
	if len(f.Fields) > 0 {
		out := make(map[string]Field, len(f.Fields))
		for k, sub := range f.Fields {
			expanded, err := inlineFieldRecursive(sub, rawAliases, resolved, visiting, chain)
			if err != nil {
				return Field{}, err
			}
			out[k] = expanded
		}
		f.Fields = out
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

// cloneField returns a deep copy of f including ElementFields and
// Enum. Enum []any was previously aliased on the assumption validation
// never mutates it, but ValidationError.Failures[].AllowedValues
// surfaces the same backing slice to callers — anyone mutating the
// failure object would corrupt the registry's cached copy and bleed
// across siblings inheriting from a shared base. Default is left as-is
// because in practice it carries primitive values; callers that
// introduce mutable Default payloads must extend this clone path too.
func cloneField(f Field) Field {
	out := f
	out.ElementFields = cloneFieldMap(f.ElementFields)
	out.Fields = cloneFieldMap(f.Fields)
	if f.Enum != nil {
		out.Enum = append([]any(nil), f.Enum...)
	}
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

func buildDB(name string, body map[string]any) (DB, []extendsRecord, error) {
	db := DB{Name: name, Types: map[string]SectionType{}}
	var extendsRecs []extendsRecord

	for key, val := range body {
		switch key {
		case metaFieldPaths:
			paths, err := stringSliceVal(name, key, val)
			if err != nil {
				return DB{}, nil, err
			}
			db.Paths = paths
		case metaFieldDescription:
			s, err := stringVal(name, key, val)
			if err != nil {
				return DB{}, nil, err
			}
			db.Description = s
		case metaFieldTypes:
			// Reserved per F21 for the named-alias table
			// [<db>.types.<alias>]. Aliases are collected up front by
			// collectAliases; here we just verify the shape is a table
			// and skip — alias bodies are not record types.
			if _, ok := val.(map[string]any); !ok {
				return DB{}, nil, fmt.Errorf(
					"schema: %s.%s: must be a table of named alias declarations, got %T",
					name, key, val)
			}
		case metaFieldBases:
			// Reserved per F22 for the named-base table
			// [<db>.bases.<name>]. Bases are collected up front by
			// collectBases; here we just verify the shape is a table
			// and skip — base bodies are not record types.
			if _, ok := val.(map[string]any); !ok {
				return DB{}, nil, fmt.Errorf(
					"schema: %s.%s: must be a table of named base declarations, got %T",
					name, key, val)
			}
		default:
			// Must be a record-type sub-table. Any scalar / non-table
			// value at this level (e.g. `format = "toml"`, the legacy
			// `file` / `directory` / `collection` keys) is an unknown
			// meta-field per F10's unknown-key contract.
			typeBody, ok := val.(map[string]any)
			if !ok {
				return DB{}, nil, fmt.Errorf(
					"schema: %s.%s: unknown meta-field or non-table value (type %T); "+
						"record types must be tables, meta-fields must be one of "+
						"paths/description/types/bases (PLAN §12.17.9 F10; F21; F22)",
					name, key, val)
			}
			st, ext, err := buildType(name, key, typeBody)
			if err != nil {
				return DB{}, nil, err
			}
			db.Types[key] = st
			if ext != "" {
				extendsRecs = append(extendsRecs, extendsRecord{
					db:      name,
					typ:     key,
					base:    ext,
					hasBase: true,
				})
			}
		}
	}

	if db.Paths == nil {
		return DB{}, nil, fmt.Errorf(
			"schema: %s: missing required %q array", name, metaFieldPaths)
	}
	if len(db.Paths) == 0 {
		return DB{}, nil, fmt.Errorf(
			"schema: %s: %q must declare at least one entry", name, metaFieldPaths)
	}

	// Format-from-extension inference + invariants per F10:
	//   - Collection mounts (trailing /, `.`) rejected outright.
	//   - Extensionless paths rejected outright.
	//   - All paths in one db must share the same recognized extension.
	var inferred Format
	for i, p := range db.Paths {
		if p == "" {
			return DB{}, nil, fmt.Errorf(
				"schema: %s: %q[%d] is empty", name, metaFieldPaths, i)
		}
		if p == "." || strings.HasSuffix(p, "/") {
			return DB{}, nil, fmt.Errorf(
				"%w: db %q path %q", ErrCollectionMountUnsupported, name, p)
		}
		f, ok := formatFromPath(p)
		if !ok {
			return DB{}, nil, fmt.Errorf(
				"%w: db %q path %q (want .toml or .md)",
				ErrAmbiguousPathFormat, name, p)
		}
		if i == 0 {
			inferred = f
			continue
		}
		if f != inferred {
			return DB{}, nil, fmt.Errorf(
				"%w: db %q paths declare both %q and %q",
				ErrInconsistentPathFormats, name, inferred, f)
		}
	}
	db.Format = inferred

	if db.Format == FormatMD {
		if err := checkRecordPerInvariants(name, db.Types); err != nil {
			return DB{}, nil, err
		}
		if err := checkMDHeadings(name, db.Types); err != nil {
			return DB{}, nil, err
		}
	} else {
		for tname, t := range db.Types {
			if t.Heading != 0 {
				return DB{}, nil, fmt.Errorf(
					"schema: %s.%s: heading only allowed when db format is %q",
					name, tname, FormatMD)
			}
			if t.RecordPer != "" {
				return DB{}, nil, fmt.Errorf(
					"%w: %s.%s carries record_per = %q",
					ErrRecordPerOnTOML, name, tname, t.RecordPer)
			}
			if t.BodyField != "" {
				return DB{}, nil, fmt.Errorf(
					"%w: %s.%s carries body_field on TOML db",
					ErrBodyFieldOnSectionType, name, tname)
			}
		}
	}

	return db, extendsRecs, nil
}

func buildType(db, name string, body map[string]any) (SectionType, string, error) {
	st := SectionType{Name: name, Fields: map[string]Field{}}
	var extendsName string

	for key, val := range body {
		switch key {
		case typeKeyDescription:
			s, err := stringVal(db+"."+name, key, val)
			if err != nil {
				return SectionType{}, "", err
			}
			st.Description = s
		case typeKeyHeading:
			n, err := intVal(db+"."+name, key, val)
			if err != nil {
				return SectionType{}, "", err
			}
			if n < 1 || n > 6 {
				return SectionType{}, "", fmt.Errorf(
					"schema: %s.%s: heading = %d invalid (must be 1..6)", db, name, n)
			}
			st.Heading = n
		case typeKeyExtends:
			s, err := stringVal(db+"."+name, key, val)
			if err != nil {
				return SectionType{}, "", err
			}
			extendsName = s
		case typeKeyFields:
			fieldsBody, ok := val.(map[string]any)
			if !ok {
				return SectionType{}, "", fmt.Errorf(
					"schema: %s.%s.fields: must be a table, got %T", db, name, val)
			}
			for fname, fval := range fieldsBody {
				fbody, ok := fval.(map[string]any)
				if !ok {
					return SectionType{}, "", fmt.Errorf(
						"schema: %s.%s.fields.%s: must be a table, got %T",
						db, name, fname, fval)
				}
				f, err := buildField(db, name, fname, fbody)
				if err != nil {
					return SectionType{}, "", err
				}
				st.Fields[fname] = f
			}
		case typeKeyAutoSpawn:
			specs, err := buildAutoSpawn(db, name, val)
			if err != nil {
				return SectionType{}, "", err
			}
			st.AutoSpawn = specs
		case typeKeyRecordPer:
			s, err := stringVal(db+"."+name, key, val)
			if err != nil {
				return SectionType{}, "", err
			}
			switch s {
			case RecordPerSection, RecordPerFile, "":
				st.RecordPer = s
			default:
				return SectionType{}, "", fmt.Errorf(
					"%w: %s.%s: got %q", ErrRecordPerInvalid, db, name, s)
			}
		case typeKeyBodyField:
			s, err := stringVal(db+"."+name, key, val)
			if err != nil {
				return SectionType{}, "", err
			}
			st.BodyField = s
		default:
			return SectionType{}, "", fmt.Errorf(
				"schema: %s.%s: unknown key %q (allowed: description, heading, fields, extends, auto_spawn, record_per, body_field)",
				db, name, key)
		}
	}

	if st.Description == "" {
		return SectionType{}, "", fmt.Errorf(
			"schema: %s.%s: missing required %q", db, name, typeKeyDescription)
	}
	// A type must declare at least one own field UNLESS it extends a
	// base — in which case the base supplies fields. Phase B.0 verifies
	// the extends target exists; if it doesn't, the resulting type
	// would have no fields at all, but the unknown-base / not-a-base
	// errors fire before validation cares.
	if len(st.Fields) == 0 && extendsName == "" {
		return SectionType{}, "", fmt.Errorf(
			"schema: %s.%s: type must declare at least one field", db, name)
	}
	return st, extendsName, nil
}

// buildAutoSpawn parses one [<db>.<type>.auto_spawn] sub-table into a
// []SpawnSpec. The sub-table must contain `on_create = [...]` whose
// entries are inline tables with `type`, `id_template`, and optional
// `fields`. Shape errors here are reported with simple messages — the
// load-phase cycle / completeness validators surface dedicated
// sentinels for cross-type concerns. Per F23.
func buildAutoSpawn(db, typeName string, val any) ([]SpawnSpec, error) {
	scope := db + "." + typeName + "." + typeKeyAutoSpawn
	body, ok := val.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"schema: %s: must be a table, got %T", scope, val)
	}
	var specs []SpawnSpec
	for key, raw := range body {
		switch key {
		case autoSpawnKeyOnCreate:
			arr, ok := raw.([]any)
			if !ok {
				return nil, fmt.Errorf(
					"schema: %s.%s: must be array of spawn specs, got %T",
					scope, autoSpawnKeyOnCreate, raw)
			}
			for i, item := range arr {
				m, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf(
						"schema: %s.%s[%d]: must be table, got %T",
						scope, autoSpawnKeyOnCreate, i, item)
				}
				spec, err := buildSpawnSpec(scope, autoSpawnKeyOnCreate, i, m)
				if err != nil {
					return nil, err
				}
				specs = append(specs, spec)
			}
		default:
			return nil, fmt.Errorf(
				"schema: %s: unknown key %q (allowed: on_create)", scope, key)
		}
	}
	return specs, nil
}

// buildSpawnSpec parses one entry of `on_create = [...]` into a
// SpawnSpec. Required keys: type, id_template. Optional: fields.
// Per-spec validity (token shape, target-type resolution) is checked
// in the post-build phases B.0.6 / B.0.7.
func buildSpawnSpec(scope, key string, idx int, body map[string]any) (SpawnSpec, error) {
	specScope := fmt.Sprintf("%s.%s[%d]", scope, key, idx)
	spec := SpawnSpec{}
	for k, v := range body {
		switch k {
		case spawnSpecKeyType:
			s, ok := v.(string)
			if !ok {
				return SpawnSpec{}, fmt.Errorf(
					"schema: %s.type: must be string, got %T", specScope, v)
			}
			spec.Type = s
		case spawnSpecKeyIDTemplate:
			s, ok := v.(string)
			if !ok {
				return SpawnSpec{}, fmt.Errorf(
					"schema: %s.id_template: must be string, got %T", specScope, v)
			}
			spec.IDTemplate = s
		case spawnSpecKeyFields:
			t, ok := v.(map[string]any)
			if !ok {
				return SpawnSpec{}, fmt.Errorf(
					"schema: %s.fields: must be table, got %T", specScope, v)
			}
			spec.Fields = t
		default:
			return SpawnSpec{}, fmt.Errorf(
				"schema: %s: unknown key %q (allowed: type, id_template, fields)",
				specScope, k)
		}
	}
	if spec.Type == "" {
		return SpawnSpec{}, fmt.Errorf(
			"schema: %s: missing required %q", specScope, spawnSpecKeyType)
	}
	if spec.IDTemplate == "" {
		return SpawnSpec{}, fmt.Errorf(
			"%w: %s: id_template is empty", ErrSpawnInvalidIDTemplate, specScope)
	}
	return spec, nil
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
		case fieldKeyFields:
			tbl, ok := val.(map[string]any)
			if !ok {
				return Field{}, fmt.Errorf(
					"schema: %s.fields: must be a table, got %T", scope, val)
			}
			subFields := make(map[string]Field, len(tbl))
			for sname, sval := range tbl {
				sbody, ok := sval.(map[string]any)
				if !ok {
					return Field{}, fmt.Errorf(
						"schema: %s.fields.%s: must be a table, got %T",
						scope, sname, sval)
				}
				sub, err := buildField(db, typeName, fname+".fields."+sname, sbody)
				if err != nil {
					return Field{}, err
				}
				subFields[sname] = sub
			}
			f.Fields = subFields
		default:
			return Field{}, fmt.Errorf(
				"schema: %s: unknown key %q (allowed: type, required, description, enum, format, default, element_type, element_fields, fields)",
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
	// `fields` (direct nested-table inner shape) is valid only when
	// type = "table". Per F28: arrays of tables use element_fields;
	// non-table fields cannot carry an inner field shape.
	if len(f.Fields) > 0 && f.Type != TypeTable {
		return Field{}, fmt.Errorf(
			"schema: %s: fields is only valid on type = \"table\" (got type %q)",
			scope, f.Type)
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
		// Per F31: file-as-record types are exempt from the heading
		// requirement. checkRecordPerInvariants has already verified
		// they carry no Heading; here we just skip them so the
		// section-mode check below stays consistent.
		if t.IsFileRecord() {
			continue
		}
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

// checkRecordPerInvariants enforces F31's per-type / per-db invariants
// for MD-format dbs:
//
//   - record_per = "file" types must NOT declare a Heading.
//   - record_per = "file" types MUST declare a body_field.
//   - body_field must reference a field declared on the type.
//   - body_field is forbidden on section-mode types.
//   - A single db cannot mix file-as-record and section-mode types.
//
// Each violation surfaces with a dedicated sentinel so callers branch
// reliably via errors.Is.
func checkRecordPerInvariants(db string, types map[string]SectionType) error {
	var sawFile, sawSection bool
	for name, t := range types {
		switch t.RecordPer {
		case RecordPerFile:
			sawFile = true
			if t.Heading != 0 {
				return fmt.Errorf(
					"%w: %s.%s declares heading=%d", ErrFileRecordWithHeading, db, name, t.Heading)
			}
			if t.BodyField == "" {
				return fmt.Errorf(
					"%w: %s.%s", ErrFileRecordMissingBodyField, db, name)
			}
			if _, ok := t.Fields[t.BodyField]; !ok {
				return fmt.Errorf(
					"%w: %s.%s.body_field=%q",
					ErrBodyFieldUnknown, db, name, t.BodyField)
			}
		default:
			// Section-mode (default or explicit "section"). body_field
			// is meaningless here — declaring it is the loud failure.
			sawSection = true
			if t.BodyField != "" {
				return fmt.Errorf(
					"%w: %s.%s.body_field=%q",
					ErrBodyFieldOnSectionType, db, name, t.BodyField)
			}
		}
	}
	if sawFile && sawSection {
		return fmt.Errorf("%w: db %q", ErrMixedRecordModes, db)
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
