package ops

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
	"github.com/evanmschultz/ta/internal/search"
)

// Ops is the Go-level (non-MCP-shaped) API the data tools use. Both
// the MCP handlers and the CLI commands route through these so the
// two surfaces stay in lockstep.

// resolveFromProjectDir routes every schema lookup through the
// package-level defaultCache. The cache stats the cascade sources on
// every call and re-resolves when any mtime has moved (V2-PLAN §4.6).
// The "path is the project directory" contract from §3 is preserved
// inside the cache's underlying loader (resolveFromProjectDirUncached).
func resolveFromProjectDir(projectPath string) (config.Resolution, error) {
	return defaultCache.Resolve(projectPath)
}

// ResolveProject is the exported V2 project-directory resolver. CLI
// and MCP entry points share this so "path is the project directory"
// holds uniformly across the tool surface. Goes through the cache so
// long-running MCP sessions don't re-stat the whole cascade on every
// call.
func ResolveProject(projectPath string) (config.Resolution, error) {
	return resolveFromProjectDir(projectPath)
}

// GetResult is the result shape returned by Get. Fields is nil unless
// the caller requested a field projection; Bytes is always populated
// with the located record's on-disk bytes.
type GetResult struct {
	FilePath string
	Bytes    []byte
	Fields   map[string]any
}

// Get reads one record. When fields is nil, GetResult.Bytes carries
// the raw bytes; when non-nil, GetResult.Fields carries the named
// field values.
func Get(path, section string, fields []string) (GetResult, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return GetResult{}, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	addr, dbDecl, err := resolver.ParseAddress(section)
	if err != nil {
		return GetResult{}, err
	}
	_, _, filePath, err := resolver.ResolveRead(section)
	if err != nil {
		return GetResult{}, err
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return GetResult{}, fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl)
	if err != nil {
		return GetResult{}, err
	}
	backendSection := backendSectionPath(dbDecl, section)
	sec, ok, err := backend.Find(buf, backendSection)
	if err != nil {
		return GetResult{}, fmt.Errorf("locate %q in %s: %w", section, filePath, err)
	}
	if !ok {
		return GetResult{}, fmt.Errorf("%w: %q in %s", ErrRecordNotFound, section, filePath)
	}
	res := GetResult{FilePath: filePath, Bytes: buf[sec.Range[0]:sec.Range[1]]}
	if len(fields) == 0 {
		return res, nil
	}
	relPath := tomlRelPathForFields(dbDecl, addr)
	out, err := extractFields(buf, sec, dbDecl, addr.Type, relPath, fields)
	if err != nil {
		return res, err
	}
	res.Fields = out
	return res, nil
}

// GetAllFields reads one record and returns ALL declared fields that
// the record carries on disk, plus the type schema so callers can build
// typed render output (render.BuildFields). Missing declared fields are
// silently omitted — this is the "no --fields specified, render
// everything" contract used by `ta get` in the B3 unified-render flow
// (V2-PLAN §12.17.5 [B3]). For MD body-only records only the "body"
// field materializes; a declared non-body MD field is skipped rather
// than erroring (cf. extractFields' strict mode, which is still the
// right contract for user-specified field lists).
func GetAllFields(path, section string) (GetResult, schema.SectionType, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return GetResult{}, schema.SectionType{}, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	addr, dbDecl, err := resolver.ParseAddress(section)
	if err != nil {
		return GetResult{}, schema.SectionType{}, err
	}
	typeSt, ok := dbDecl.Types[addr.Type]
	if !ok {
		return GetResult{}, schema.SectionType{}, fmt.Errorf("%w: type %q not declared on db %q", ErrUnknownField, addr.Type, dbDecl.Name)
	}
	_, _, filePath, err := resolver.ResolveRead(section)
	if err != nil {
		return GetResult{}, typeSt, err
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return GetResult{}, typeSt, fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl)
	if err != nil {
		return GetResult{}, typeSt, err
	}
	backendSection := backendSectionPath(dbDecl, section)
	sec, found, err := backend.Find(buf, backendSection)
	if err != nil {
		return GetResult{}, typeSt, fmt.Errorf("locate %q in %s: %w", section, filePath, err)
	}
	if !found {
		return GetResult{}, typeSt, fmt.Errorf("%w: %q in %s", ErrRecordNotFound, section, filePath)
	}
	res := GetResult{FilePath: filePath, Bytes: buf[sec.Range[0]:sec.Range[1]]}
	relPath := tomlRelPathForFields(dbDecl, addr)
	out, err := extractAllDeclaredFields(buf, sec, dbDecl, typeSt, relPath)
	if err != nil {
		return res, typeSt, err
	}
	res.Fields = out
	return res, typeSt, nil
}

// Create creates a new record. Returns the absolute file path that
// was written plus the resolved schema source list for diagnostics.
// Fails with ErrRecordExists when the record already exists.
func Create(path, section, pathHint string, data map[string]any) (string, []string, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	if err := resolution.Registry.Validate(validationPath(resolution.Registry, section), data); err != nil {
		return "", nil, err
	}
	resolver := db.NewResolver(path, resolution.Registry)
	_, dbDecl, err := resolver.ParseAddress(section)
	if err != nil {
		return "", nil, err
	}
	_, _, filePath, err := resolver.ResolveWrite(section, pathHint)
	if err != nil {
		return "", nil, err
	}
	backend, err := buildBackend(dbDecl)
	if err != nil {
		return "", nil, err
	}
	buf, err := readFileIfExists(filePath)
	if err != nil {
		return "", nil, err
	}
	backendSection := backendSectionPath(dbDecl, section)
	if _, exists, err := backend.Find(buf, backendSection); err != nil {
		return "", nil, fmt.Errorf("pre-create probe %q: %w", section, err)
	} else if exists {
		return "", nil, fmt.Errorf("%w: %q", ErrRecordExists, section)
	}
	emitted, err := backend.Emit(backendSection, record.Record(data))
	if err != nil {
		return "", nil, fmt.Errorf("emit %q: %w", section, err)
	}
	newBuf, err := backend.Splice(buf, backendSection, emitted)
	if err != nil {
		return "", nil, fmt.Errorf("splice %q: %w", section, err)
	}
	// Track whether we just created the instance dir so a WriteAtomic
	// failure after MkdirAll succeeds does not leave orphan state on
	// disk. os.Stat is best-effort; if it fails we skip the cleanup but
	// still surface the write error.
	dir := filepath.Dir(filePath)
	dirCreated := false
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		dirCreated = true
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := toml.WriteAtomic(filePath, newBuf); err != nil {
		if dirCreated {
			// Roll back the mkdir only when the dir we created is still
			// empty — never prune a dir that already had siblings.
			if entries, lstErr := os.ReadDir(dir); lstErr == nil && len(entries) == 0 {
				_ = os.Remove(dir)
			}
		}
		return "", nil, err
	}
	return filePath, resolution.Sources, nil
}

// Update applies a PATCH-style partial overlay to an existing record
// (V2-PLAN §3.5, §12.17.5 [B1]). The incoming data map is NOT a full
// replacement: provided fields overwrite their stored values; unspecified
// fields retain their on-disk values byte-identically after the merged
// record is re-emitted.
//
// Null-handling per the spec:
//
//   - `{"field": null}` on a NOT-required field removes the field from
//     the merged record (and therefore from the emitted bytes).
//   - `{"field": null}` on a required field with NO schema default
//     returns ErrCannotClearRequired — required fields cannot be unset
//     via Update.
//   - `{"field": null}` on a required field WITH a schema default
//     replaces the field with the declared default value at write time
//     ("write-time freeze"; later schema default-value edits do not
//     retroactively update existing records).
//
// Empty data (`{}`) short-circuits before overlay or re-validation: the
// caller gets a clean success response and the on-disk bytes are not
// touched. `update` is not a validator; if the stored record is
// malformed, surface that on the next read, not here.
//
// After overlay, the merged record is validated against the type
// schema. Any field-level validation failure rejects the whole update
// atomically; the on-disk bytes are unchanged.
//
// Fails with ErrFileNotFound when the backing file does not exist. A
// missing record in an existing file continues to be upserted (append)
// under the non-empty-data path, matching the pre-PATCH behavior; empty
// data on a missing record is a no-op and does not append.
func Update(path, section string, data map[string]any) (string, []string, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	addr, dbDecl, err := resolver.ParseAddress(section)
	if err != nil {
		return "", nil, err
	}
	_, _, filePath, err := resolver.ResolveRead(section)
	if err != nil {
		return "", nil, err
	}
	// Empty-data short-circuit (§3.5 / §12.17.5 [B1]): confirm the file
	// exists, then return a clean success without reading the file
	// body, without re-validating the stored record, and without
	// touching disk.
	if len(data) == 0 {
		if _, err := os.Stat(filePath); err != nil {
			if os.IsNotExist(err) {
				return "", nil, fmt.Errorf("%w: %s", ErrFileNotFound, filePath)
			}
			return "", nil, fmt.Errorf("stat %s: %w", filePath, err)
		}
		return filePath, resolution.Sources, nil
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("%w: %s", ErrFileNotFound, filePath)
		}
		return "", nil, fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl)
	if err != nil {
		return "", nil, err
	}
	backendSection := backendSectionPath(dbDecl, section)

	// Load the existing record's declared fields into a map we can
	// overlay onto. When the record is absent the merged map starts
	// empty — pre-PATCH upsert-within-file semantics are preserved.
	st, ok := dbDecl.Types[addr.Type]
	if !ok {
		return "", nil, fmt.Errorf("%w: type %q on db %q",
			ErrUnknownField, addr.Type, dbDecl.Name)
	}
	existing, err := loadExistingFields(buf, backend, backendSection, dbDecl, addr, st)
	if err != nil {
		return "", nil, err
	}
	merged, err := overlayPatch(existing, data, st)
	if err != nil {
		return "", nil, err
	}
	if err := resolution.Registry.Validate(validationPath(resolution.Registry, section), merged); err != nil {
		return "", nil, err
	}

	emitted, err := backend.Emit(backendSection, record.Record(merged))
	if err != nil {
		return "", nil, fmt.Errorf("emit %q: %w", section, err)
	}
	newBuf, err := backend.Splice(buf, backendSection, emitted)
	if err != nil {
		return "", nil, fmt.Errorf("splice %q: %w", section, err)
	}
	if err := toml.WriteAtomic(filePath, newBuf); err != nil {
		return "", nil, err
	}
	return filePath, resolution.Sources, nil
}

// loadExistingFields returns the declared-field values currently
// stored for the record located by backendSection, or an empty map if
// the record does not yet exist in the backing file. Only keys
// declared on the type's schema are surfaced.
func loadExistingFields(buf []byte, backend record.Backend, backendSection string, dbDecl schema.DB, addr db.Address, st schema.SectionType) (map[string]any, error) {
	sec, ok, err := backend.Find(buf, backendSection)
	if err != nil {
		return nil, fmt.Errorf("locate %q: %w", backendSection, err)
	}
	if !ok {
		return map[string]any{}, nil
	}
	relPath := tomlRelPathForFields(dbDecl, addr)
	declaredNames := make([]string, 0, len(st.Fields))
	for name := range st.Fields {
		declaredNames = append(declaredNames, name)
	}
	out, err := extractFields(buf, sec, dbDecl, addr.Type, relPath, declaredNames)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// overlayPatch applies PATCH semantics to existing: each key in patch
// overwrites the corresponding entry in a clone of existing. A nil
// patch value triggers null-clear rules (§3.5):
//
//   - not-required field → key removed from the merged map.
//   - required field with schema default → key replaced with the
//     declared default value (literal write at update time).
//   - required field with no schema default → ErrCannotClearRequired.
//
// Unknown-in-patch names with a non-nil value are passed through so
// schema.Validate can surface them with its canonical unknown-field
// failure. Unknown-in-patch names with a nil value are dropped (Emit
// cannot serialize nil and schema.Validate would not run on it).
func overlayPatch(existing, patch map[string]any, st schema.SectionType) (map[string]any, error) {
	merged := make(map[string]any, len(existing)+len(patch))
	maps.Copy(merged, existing)
	for name, val := range patch {
		if val != nil {
			merged[name] = val
			continue
		}
		field, declared := st.Fields[name]
		if !declared {
			continue
		}
		if !field.Required {
			delete(merged, name)
			continue
		}
		if field.Default == nil {
			return nil, fmt.Errorf("%w: %q", ErrCannotClearRequired, name)
		}
		merged[name] = field.Default
	}
	return merged, nil
}

// Delete removes a record, a whole single-instance data file, or a
// multi-instance instance directory / file. Whole multi-instance db
// deletes fail with ErrAmbiguousDelete.
func Delete(path, section string) (string, []string, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	removed, handled, err := deleteAtLevel(path, section, resolution)
	if err != nil {
		return "", resolution.Sources, err
	}
	if handled {
		return removed, resolution.Sources, nil
	}
	// Record-level delete.
	resolver := db.NewResolver(path, resolution.Registry)
	_, dbDecl, err := resolver.ParseAddress(section)
	if err != nil {
		return "", nil, err
	}
	_, _, filePath, err := resolver.ResolveRead(section)
	if err != nil {
		return "", nil, err
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("%w: %s", ErrFileNotFound, filePath)
		}
		return "", nil, fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl)
	if err != nil {
		return "", nil, err
	}
	backendSection := backendSectionPath(dbDecl, section)
	sec, ok, err := backend.Find(buf, backendSection)
	if err != nil {
		return "", nil, fmt.Errorf("locate %q: %w", section, err)
	}
	if !ok {
		return "", nil, fmt.Errorf("%w: %q", ErrRecordNotFound, section)
	}
	newBuf := spliceOut(buf, sec.Range)
	if err := toml.WriteAtomic(filePath, newBuf); err != nil {
		return "", nil, err
	}
	return filePath, resolution.Sources, nil
}

// SearchHit mirrors search.Result at the ops boundary so callers
// (MCP handler, CLI subcommand) can depend on ops alone.
type SearchHit struct {
	Section string
	Bytes   []byte
	Fields  map[string]any
}

// defaultListLimit is the endpoint-enforced default cap on ListSections
// and Search when the caller passes limit <= 0 && all == false, per
// docs/PLAN.md §3.2 / §3.7 / §12.17.5 [A2.1]+[A2.2]. The MCP surface
// today is uncapped; moving the default into the endpoint means MCP
// agents now also see 10-by-default unless they pass all=true or an
// explicit limit. This is the F1-asymmetry fix §12.17.5 calls out —
// release-note material, not a regression.
const defaultListLimit = 10

// resolveLimit applies the default-10 / all-wins rules for the endpoint
// cap. Adapters (CLI cobra mutex, MCP handler guard) enforce the UX
// "pass either limit or all, not both" rule; at the endpoint we stay
// permissive — all == true beats any non-zero limit so library callers
// see deterministic precedence.
func resolveLimit(limit int, all bool) int {
	if all {
		return 0
	}
	if limit > 0 {
		return limit
	}
	return defaultListLimit
}

// ListSections enumerates every record address reachable under `scope`
// as full project-level dotted addresses (`<db>.<type>.<id-path>` for
// single-instance dbs, `<db>.<instance>.<type>.<id-path>` for multi-
// instance dbs). Scope grammar mirrors search (`<db>` | `<db>.<type>`
// | `<db>.<instance>` | `<db>.<type>.<id-prefix>` |
// `<db>.<instance>.<type>(.<id-prefix>)?`); empty scope walks the whole
// project. Mirrors docs/PLAN.md §3.2 and the §12.17.5 [A2] CLI rewrite.
//
// `limit` caps the returned address count; `all == true` returns every
// address in scope (limit ignored). `limit <= 0 && all == false`
// substitutes the endpoint default (`defaultListLimit` = 10). CLI and
// MCP adapters pass their incoming flag/param values through verbatim;
// the endpoint owns the default. §12.17.5 [A2.1].
//
// Implemented as a zero-filter search: the walker in internal/search
// already produces full addresses in file-parse order, so routing
// through it keeps the address shape in lockstep with `search` and
// `get` (§3.1 scope expansion).
func ListSections(path, scope string, limit int, all bool) ([]string, error) {
	results, err := search.Run(search.Query{
		Path:  path,
		Scope: scope,
		Limit: resolveLimit(limit, all),
		All:   all,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Section
	}
	return out, nil
}

// ScopeRecord is one record returned by GetScope. Mirrors SearchHit's
// shape — full dotted address, raw on-disk bytes, and the decoded field
// map for every declared field on the record type. Section is the
// caller-visible address; Fields reflects the ops.GetScope `fields`
// filter (nil = every declared field; non-nil = named subset with any
// absent names silently omitted, matching search's optional-field
// contract).
//
// GetScope is the multi-record half of §12.17.5 [B2]. Single-record
// `ta get` callers continue to route through ops.Get and see the
// unchanged GetResult shape (§12.17.5 [B2] backwards-compat lock).
type ScopeRecord struct {
	Section string
	Bytes   []byte
	Fields  map[string]any
}

// IsScopeAddress reports whether section is a scope-prefix address
// (<db>, <db>.<type> single-instance, <db>.<instance>, or
// <db>.<instance>.<type> multi-instance) rather than a fully-qualified
// single-record address (<db>.<type>.<id> single-instance,
// <db>.<instance>.<type>.<id> multi-instance).
//
// Detection is segment-count + db-shape only — no I/O beyond the
// schema-resolve the caller already does for the routing decision. An
// unknown db returns an error so the caller surfaces a loud typo
// failure rather than pretending the address was "just a scope we
// couldn't walk".
//
// Mirrors the grammar search.parseScope / list_sections already accept
// (V2-PLAN §3.1 amendment 2026-04-23 / §12.17.5 [B2]).
func IsScopeAddress(path, section string) (bool, error) {
	if section == "" {
		return false, fmt.Errorf("%w: empty section", search.ErrInvalidScope)
	}
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return false, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	parts := strings.Split(section, ".")
	if slices.Contains(parts, "") {
		return false, fmt.Errorf("%w: %q has empty segment", search.ErrInvalidScope, section)
	}
	dbDecl, ok := resolution.Registry.DBs[parts[0]]
	if !ok {
		return false, fmt.Errorf("%w: unknown db %q", search.ErrInvalidScope, parts[0])
	}
	switch dbDecl.Shape {
	case schema.ShapeFile:
		// single-instance: 1 seg (<db>) and 2 segs (<db>.<type>) are
		// scopes; 3+ segs (<db>.<type>.<id-path>) is single-record.
		return len(parts) < 3, nil
	default:
		// multi-instance: 1 seg (<db>), 2 segs (<db>.<instance>), and
		// 3 segs (<db>.<instance>.<type>) are scopes; 4+ segs
		// (<db>.<instance>.<type>.<id-path>) is single-record.
		return len(parts) < 4, nil
	}
}

// GetScope enumerates every record under a scope-prefix section and
// returns them in file-parse order. Fields filters the returned Fields
// map per record (nil = every declared field). Limit caps the record
// count with the same default-10 / all-wins contract as Search /
// ListSections (see resolveLimit). Mirrors §12.17.5 [B2].
//
// Routes through search.Run with zero match/query/field filters so the
// walker reuses one code path for every scope-expansion endpoint. The
// CLI/MCP adapters enforce the `limit`/`all` mutex; the endpoint stays
// permissive (all-wins) for library-caller determinism.
func GetScope(path, section string, fields []string, limit int, all bool) ([]ScopeRecord, error) {
	results, err := search.Run(search.Query{
		Path:  path,
		Scope: section,
		Limit: resolveLimit(limit, all),
		All:   all,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ScopeRecord, len(results))
	for i, r := range results {
		out[i] = ScopeRecord{
			Section: r.Section,
			Bytes:   r.Bytes,
			Fields:  filterFields(r.Fields, fields),
		}
	}
	return out, nil
}

// filterFields narrows values to the names subset. Empty names passes
// through every decoded field; unknown names in `names` are simply
// absent from the output (mirrors search's optional-field contract —
// a named field that does not appear on the record type is not an
// error at the scope-expansion level, it just doesn't materialize).
func filterFields(values map[string]any, names []string) map[string]any {
	if len(names) == 0 {
		return values
	}
	out := make(map[string]any, len(names))
	for _, n := range names {
		if v, ok := values[n]; ok {
			out[n] = v
		}
	}
	return out
}

// Search executes a ta `search` query. scope, match, queryRegex, and
// field are optional. queryRegex is compiled with regexp.Compile — pass
// "" to skip the regex pass. `limit` caps the returned hit count;
// `all == true` returns every hit. Default-10 / all-wins contract is
// identical to ListSections (see resolveLimit). Mirrors docs/PLAN.md
// §3.7 / §12.17.5 [A2.2].
func Search(path, scope string, match map[string]any, queryRegex, field string, limit int, all bool) ([]SearchHit, error) {
	q := search.Query{
		Path:  path,
		Scope: scope,
		Match: match,
		Field: field,
		Limit: resolveLimit(limit, all),
		All:   all,
	}
	if queryRegex != "" {
		re, err := regexp.Compile(queryRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", queryRegex, err)
		}
		q.Query = re
	}
	results, err := search.Run(q)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, len(results))
	for i, r := range results {
		out[i] = SearchHit{Section: r.Section, Bytes: r.Bytes, Fields: r.Fields}
	}
	return out, nil
}

// deleteAtLevel handles the coarser-than-record delete forms per §3.6.
// Returns (removedPath, true, nil) on success at those levels,
// ("", false, nil) when the caller should fall through to record-level
// handling, or an error for coarse-level failures.
func deleteAtLevel(path, section string, resolution config.Resolution) (string, bool, error) {
	segs := strings.Split(section, ".")
	if len(segs) == 0 || segs[0] == "" {
		return "", true, errors.New("empty section")
	}
	dbDecl, ok := resolution.Registry.DBs[segs[0]]
	if !ok {
		// Let the record-level path produce the canonical error.
		return "", false, nil
	}
	switch len(segs) {
	case 1:
		if dbDecl.Shape != schema.ShapeFile {
			return "", true, fmt.Errorf(
				"%w: db %q is %s; delete each instance first or use schema(action=delete, kind=db)",
				ErrAmbiguousDelete, dbDecl.Name, dbDecl.Shape)
		}
		target := filepath.Join(path, dbDecl.Path)
		if err := os.Remove(target); err != nil {
			if os.IsNotExist(err) {
				return "", true, fmt.Errorf("%w: %s", ErrFileNotFound, target)
			}
			return "", true, fmt.Errorf("remove %s: %w", target, err)
		}
		return target, true, nil
	case 2:
		if dbDecl.Shape == schema.ShapeFile {
			return "", true, fmt.Errorf(
				"single-instance db %q has no instances; use section=%q for whole-db delete",
				dbDecl.Name, dbDecl.Name)
		}
		resolver := db.NewResolver(path, resolution.Registry)
		instances, err := resolver.Instances(dbDecl.Name)
		if err != nil {
			return "", true, err
		}
		var target *db.Instance
		for i := range instances {
			if instances[i].Slug == segs[1] {
				target = &instances[i]
				break
			}
		}
		if target == nil {
			return "", true, fmt.Errorf("instance %q of db %q not found", segs[1], dbDecl.Name)
		}
		if dbDecl.Shape == schema.ShapeDirectory {
			if err := os.RemoveAll(target.DirPath); err != nil {
				return "", true, fmt.Errorf("remove %s: %w", target.DirPath, err)
			}
			return target.DirPath, true, nil
		}
		if err := os.Remove(target.FilePath); err != nil {
			return "", true, fmt.Errorf("remove %s: %w", target.FilePath, err)
		}
		return target.FilePath, true, nil
	default:
		// Record-level — caller handles.
		return "", false, nil
	}
}
