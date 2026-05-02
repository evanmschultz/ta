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

// Ops is the Go-level (non-MCP-shaped) API the data tools use.

func resolveFromProjectDir(projectPath string) (config.Resolution, error) {
	return defaultCache.Resolve(projectPath)
}

// ResolveProject is the exported V2 project-directory resolver.
func ResolveProject(projectPath string) (config.Resolution, error) {
	return resolveFromProjectDir(projectPath)
}

// GetResult is the result shape returned by Get.
type GetResult struct {
	FilePath string
	Bytes    []byte
	Fields   map[string]any
}

// declaredTypeNames returns dbDecl's declared type names as a set.
func declaredTypeNames(dbDecl schema.DB) map[string]struct{} {
	out := make(map[string]struct{}, len(dbDecl.Types))
	for n := range dbDecl.Types {
		out[n] = struct{}{}
	}
	return out
}

// Get reads one record. Per F10 (PLAN §12.17.9): type is OPTIONAL on
// read paths; the index is the authoritative type source. typeName,
// when non-empty, MUST be db-qualified (e.g. `plans.task`); a bare
// slug surfaces ErrTypeNotQualified.
func Get(path, id, typeName string, fields []string) (GetResult, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return GetResult{}, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	resolved, dbDecl, err := resolver.ResolveID(id)
	if err != nil {
		return GetResult{}, err
	}
	bareType, err := resolveTypeForID(resolved, typeName, false, path, declaredTypeNames(dbDecl))
	if err != nil {
		return GetResult{}, err
	}
	_, _, filePath, err := resolver.ResolveRead(id)
	if err != nil {
		return GetResult{}, err
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return GetResult{}, fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl, resolved)
	if err != nil {
		return GetResult{}, err
	}
	backendSection := backendSectionPath(dbDecl, resolved)
	sec, ok, err := backend.Find(buf, backendSection)
	if err != nil {
		return GetResult{}, fmt.Errorf("locate %q in %s: %w", id, filePath, err)
	}
	if !ok {
		return GetResult{}, fmt.Errorf("%w: %q in %s", ErrRecordNotFound, id, filePath)
	}
	res := GetResult{FilePath: filePath, Bytes: buf[sec.Range[0]:sec.Range[1]]}
	if len(fields) == 0 {
		return res, nil
	}
	relPath := tomlRelPathForFields(resolved)
	out, err := extractFields(buf, sec, dbDecl, bareType, relPath, fields)
	if err != nil {
		return res, err
	}
	res.Fields = out
	return res, nil
}

// GetAllFields reads one record and returns ALL declared fields.
func GetAllFields(path, id, typeName string) (GetResult, schema.SectionType, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return GetResult{}, schema.SectionType{}, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	resolved, dbDecl, err := resolver.ResolveID(id)
	if err != nil {
		return GetResult{}, schema.SectionType{}, err
	}
	bareType, err := resolveTypeForID(resolved, typeName, false, path, declaredTypeNames(dbDecl))
	if err != nil {
		return GetResult{}, schema.SectionType{}, err
	}
	typeSt, ok := dbDecl.Types[bareType]
	if !ok {
		return GetResult{}, schema.SectionType{}, fmt.Errorf("%w: type %q not declared on db %q", ErrUnknownField, bareType, dbDecl.Name)
	}
	_, _, filePath, err := resolver.ResolveRead(id)
	if err != nil {
		return GetResult{}, typeSt, err
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return GetResult{}, typeSt, fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl, resolved)
	if err != nil {
		return GetResult{}, typeSt, err
	}
	backendSection := backendSectionPath(dbDecl, resolved)
	sec, found, err := backend.Find(buf, backendSection)
	if err != nil {
		return GetResult{}, typeSt, fmt.Errorf("locate %q in %s: %w", id, filePath, err)
	}
	if !found {
		return GetResult{}, typeSt, fmt.Errorf("%w: %q in %s", ErrRecordNotFound, id, filePath)
	}
	res := GetResult{FilePath: filePath, Bytes: buf[sec.Range[0]:sec.Range[1]]}
	relPath := tomlRelPathForFields(resolved)
	out, err := extractAllDeclaredFields(buf, sec, dbDecl, typeSt, relPath)
	if err != nil {
		return res, typeSt, err
	}
	res.Fields = out
	return res, typeSt, nil
}

// Create creates a new record. typeName is REQUIRED and must be
// db-qualified (`<db>.<type>`).
func Create(path, id, typeName string, data map[string]any) (string, []string, error) {
	if typeName == "" {
		return "", nil, fmt.Errorf("%w: create requires --type (db-qualified, e.g. `plans.task`)", ErrTypeMismatch)
	}
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	resolved, dbDecl, err := resolver.ResolveID(id)
	if err != nil {
		return "", nil, err
	}
	bareType, err := resolveTypeForID(resolved, typeName, true, path, declaredTypeNames(dbDecl))
	if err != nil {
		return "", nil, err
	}
	if err := resolution.Registry.Validate(validationPath(resolved, bareType), data); err != nil {
		return "", nil, err
	}
	_, _, filePath, err := resolver.ResolveWrite(id, "")
	if err != nil {
		return "", nil, err
	}
	backend, err := buildBackend(dbDecl, resolved)
	if err != nil {
		return "", nil, err
	}
	buf, err := readFileIfExists(filePath)
	if err != nil {
		return "", nil, err
	}
	backendSection := backendSectionPath(dbDecl, resolved)
	if _, exists, err := backend.Find(buf, backendSection); err != nil {
		return "", nil, fmt.Errorf("pre-create probe %q: %w", id, err)
	} else if exists {
		return "", nil, fmt.Errorf("%w: %q", ErrRecordExists, id)
	}
	emitted, err := backend.Emit(backendSection, record.Record(data))
	if err != nil {
		return "", nil, fmt.Errorf("emit %q: %w", id, err)
	}
	newBuf, err := backend.Splice(buf, backendSection, emitted)
	if err != nil {
		return "", nil, fmt.Errorf("splice %q: %w", id, err)
	}
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
			if entries, lstErr := os.ReadDir(dir); lstErr == nil && len(entries) == 0 {
				_ = os.Remove(dir)
			}
		}
		return "", nil, err
	}
	if err := writeIndexEntry(path, resolved, bareType); err != nil {
		return filePath, resolution.Sources, err
	}
	return filePath, resolution.Sources, nil
}

// Update applies a PATCH-style partial overlay to an existing record.
func Update(path, id, typeName string, data map[string]any) (string, []string, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	resolved, dbDecl, err := resolver.ResolveID(id)
	if err != nil {
		return "", nil, err
	}
	// File-existence first so a missing backing file surfaces as
	// ErrFileNotFound rather than as ErrIndexMissing / ErrTypeUnresolved.
	_, _, filePath, err := resolver.ResolveRead(id)
	if err != nil {
		// ResolveRead returns ErrInstanceNotFound when the file is
		// missing; surface that as ErrFileNotFound for parity with the
		// pre-F10 contract.
		if errors.Is(err, db.ErrInstanceNotFound) {
			return "", nil, fmt.Errorf("%w: %v", ErrFileNotFound, err)
		}
		return "", nil, err
	}
	bareType, err := resolveTypeForID(resolved, typeName, false, path, declaredTypeNames(dbDecl))
	if err != nil {
		return "", nil, err
	}
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
	backend, err := buildBackend(dbDecl, resolved)
	if err != nil {
		return "", nil, err
	}
	backendSection := backendSectionPath(dbDecl, resolved)

	st, ok := dbDecl.Types[bareType]
	if !ok {
		return "", nil, fmt.Errorf("%w: type %q on db %q",
			ErrUnknownField, bareType, dbDecl.Name)
	}
	existing, err := loadExistingFields(buf, backend, backendSection, dbDecl, resolved, bareType, st)
	if err != nil {
		return "", nil, err
	}
	merged, err := overlayPatch(existing, data, st)
	if err != nil {
		return "", nil, err
	}
	if err := resolution.Registry.Validate(validationPath(resolved, bareType), merged); err != nil {
		return "", nil, err
	}

	emitted, err := backend.Emit(backendSection, record.Record(merged))
	if err != nil {
		return "", nil, fmt.Errorf("emit %q: %w", id, err)
	}
	newBuf, err := backend.Splice(buf, backendSection, emitted)
	if err != nil {
		return "", nil, fmt.Errorf("splice %q: %w", id, err)
	}
	if err := toml.WriteAtomic(filePath, newBuf); err != nil {
		return "", nil, err
	}
	if err := writeIndexEntry(path, resolved, bareType); err != nil {
		return filePath, resolution.Sources, err
	}
	return filePath, resolution.Sources, nil
}

func loadExistingFields(buf []byte, backend record.Backend, backendSection string, dbDecl schema.DB, resolved db.Resolved, bareType string, st schema.SectionType) (map[string]any, error) {
	sec, ok, err := backend.Find(buf, backendSection)
	if err != nil {
		return nil, fmt.Errorf("locate %q: %w", backendSection, err)
	}
	if !ok {
		return map[string]any{}, nil
	}
	relPath := tomlRelPathForFields(resolved)
	declaredNames := make([]string, 0, len(st.Fields))
	for name := range st.Fields {
		declaredNames = append(declaredNames, name)
	}
	out, err := extractFields(buf, sec, dbDecl, bareType, relPath, declaredNames)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

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

// DeleteOptions controls optional behavior of DeleteWithOptions. Force
// authorizes file-level delete (whole-file removal) when the resolved
// id addresses one concrete file rather than one record. Verbose
// requests post-delete verification of the file's remaining-record
// count and is honored by the caller (CLI / MCP) rather than by the
// ops layer; the field exists on the options struct so both surfaces
// share one shape.
type DeleteOptions struct {
	// Force authorizes file-level delete (whole-file removal) without
	// an interactive confirmation. Required for file-level delete on
	// non-TTY callers (and for the MCP delete tool unconditionally,
	// since MCP has no TTY available). Ignored for record-level delete.
	Force bool

	// Verbose has no behavioral effect inside ops; it is propagated
	// onto DeleteResult so callers can render extra detail. Kept on
	// DeleteOptions so CLI / MCP wiring stays symmetric.
	Verbose bool
}

// DeleteResult is the structured result of DeleteWithOptions. FilePath
// is the absolute file path of the affected file (the file the deleted
// record lived in for record-level delete, the deleted file for
// file-level delete). Sources is the schema-source provenance list,
// same as other mutation endpoints. Level is the resolved delete level
// (record or file). RemainingInFile is the post-delete count of
// records remaining in the file, sourced from the index.
type DeleteResult struct {
	FilePath        string
	Sources         []string
	Level           db.DeleteLevel
	RemainingInFile int
}

// Delete is the legacy 3-return-value entry point retained for
// callers that do not need the file-level path. It is a thin shim over
// DeleteWithOptions(zero opts). Force defaults to false, so file-level
// ids hit ErrFileDeleteRequiresForce — which is the correct safe
// default for any caller still on the old shape.
func Delete(path, id, typeName string) (string, []string, error) {
	res, err := DeleteWithOptions(path, id, typeName, DeleteOptions{})
	return res.FilePath, res.Sources, err
}

// DeleteWithOptions removes a record or a whole file under F19. The id
// resolves to one of three levels via db.Resolver.ResolveDelete:
//
//   - LevelRecord (full id with bracket-key): splice the record bytes
//     out of the backing file and remove the index entry. Same as the
//     pre-F19 contract.
//   - LevelFile (bare file-relpath that uniquely identifies one
//     concrete file): delete the file from disk, then prune every
//     index entry whose canonical id begins with `<file-relpath>.`.
//     Requires opts.Force=true; otherwise ErrFileDeleteRequiresForce
//     fires before any disk mutation. The CLI gates the prompt off-TTY
//     and converts an interactive `huh.Confirm=true` into Force=true;
//     the MCP delete tool requires force=true unconditionally because
//     it has no TTY available.
//   - LevelGlobRoot (bare file-relpath that resolves to multiple
//     concrete files via a glob mount): refuses with
//     ErrUnscopedGlobDelete; the caller must narrow the id.
//
// File-level delete is non-atomic by design (per F19 dev decision):
// os.Remove first, then index cleanup. On disk-remove success + index
// cleanup failure the wrapped error includes the "file removed; run
// `ta index rebuild`" recovery hint — paralleling the existing
// record-level-delete semantics.
func DeleteWithOptions(path, id, typeName string, opts DeleteOptions) (DeleteResult, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	resolved, dbDecl, level, err := resolver.ResolveDelete(id)
	if err != nil {
		if errors.Is(err, db.ErrUnscopedGlobDelete) {
			return DeleteResult{Sources: resolution.Sources, Level: db.LevelGlobRoot},
				fmt.Errorf("%w: %v", ErrUnscopedGlobDelete, err)
		}
		return DeleteResult{Sources: resolution.Sources}, err
	}

	switch level {
	case db.LevelRecord:
		return deleteRecord(path, resolution.Sources, resolver, dbDecl, resolved, id, typeName)
	case db.LevelFile:
		if !opts.Force {
			return DeleteResult{Sources: resolution.Sources, Level: db.LevelFile, FilePath: resolved.FilePath},
				fmt.Errorf("%w: %q addresses one whole file (%s)",
					ErrFileDeleteRequiresForce, id, resolved.FilePath)
		}
		return deleteFile(path, resolution.Sources, resolved)
	default:
		// LevelGlobRoot is handled by the err branch above; any other
		// value is a programmer error.
		return DeleteResult{Sources: resolution.Sources}, fmt.Errorf(
			"ops: DeleteWithOptions: unexpected level %d for id %q", level, id)
	}
}

// deleteRecord is the record-level delete path. It mirrors the pre-F19
// flow: validate type cross-check (when typeName is supplied), splice
// the record bytes out of the backing file, prune the index entry,
// then return the post-delete count of records remaining in the same
// file (file-scoped, per F20 lock).
func deleteRecord(path string, sources []string, resolver *db.Resolver, dbDecl schema.DB, resolved db.Resolved, id, typeName string) (DeleteResult, error) {
	if _, err := resolveTypeForID(resolved, typeName, false, path, declaredTypeNames(dbDecl)); err != nil {
		return DeleteResult{Sources: sources, Level: db.LevelRecord}, err
	}
	_, _, filePath, err := resolver.ResolveRead(id)
	if err != nil {
		return DeleteResult{Sources: sources, Level: db.LevelRecord}, err
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return DeleteResult{Sources: sources, Level: db.LevelRecord},
				fmt.Errorf("%w: %s", ErrFileNotFound, filePath)
		}
		return DeleteResult{Sources: sources, Level: db.LevelRecord},
			fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl, resolved)
	if err != nil {
		return DeleteResult{Sources: sources, Level: db.LevelRecord}, err
	}
	backendSection := backendSectionPath(dbDecl, resolved)
	sec, ok, err := backend.Find(buf, backendSection)
	if err != nil {
		return DeleteResult{Sources: sources, Level: db.LevelRecord},
			fmt.Errorf("locate %q: %w", id, err)
	}
	if !ok {
		return DeleteResult{Sources: sources, Level: db.LevelRecord},
			fmt.Errorf("%w: %q", ErrRecordNotFound, id)
	}
	newBuf := spliceOut(buf, sec.Range)
	if err := toml.WriteAtomic(filePath, newBuf); err != nil {
		return DeleteResult{Sources: sources, Level: db.LevelRecord}, err
	}
	if err := deleteIndexEntry(path, resolved); err != nil {
		return DeleteResult{FilePath: filePath, Sources: sources, Level: db.LevelRecord}, err
	}
	remaining, _ := countRecordsInFile(path, resolved.FileRelPath)
	return DeleteResult{
		FilePath:        filePath,
		Sources:         sources,
		Level:           db.LevelRecord,
		RemainingInFile: remaining,
	}, nil
}

// deleteFile is the file-level delete path. Per F19's locked
// non-atomic semantics: os.Remove first, then prune every index entry
// whose canonical id begins with `<file-relpath>.`. A cleanup failure
// after a successful disk remove returns a wrapped error with the
// `ta index rebuild` recovery hint.
func deleteFile(path string, sources []string, resolved db.Resolved) (DeleteResult, error) {
	if _, err := os.Stat(resolved.FilePath); err != nil {
		if os.IsNotExist(err) {
			return DeleteResult{Sources: sources, Level: db.LevelFile, FilePath: resolved.FilePath},
				fmt.Errorf("%w: %s", ErrFileNotFound, resolved.FilePath)
		}
		return DeleteResult{Sources: sources, Level: db.LevelFile, FilePath: resolved.FilePath},
			fmt.Errorf("stat %s: %w", resolved.FilePath, err)
	}
	if err := os.Remove(resolved.FilePath); err != nil {
		return DeleteResult{Sources: sources, Level: db.LevelFile, FilePath: resolved.FilePath},
			fmt.Errorf("remove %s: %w", resolved.FilePath, err)
	}
	if err := deleteIndexEntriesByFile(path, resolved.FileRelPath); err != nil {
		return DeleteResult{FilePath: resolved.FilePath, Sources: sources, Level: db.LevelFile}, err
	}
	// Post-delete the file is gone; remaining-in-file count is zero by
	// definition. Return the explicit zero so the verbose path can
	// surface it without a special case.
	return DeleteResult{
		FilePath:        resolved.FilePath,
		Sources:         sources,
		Level:           db.LevelFile,
		RemainingInFile: 0,
	}, nil
}

// SearchHit mirrors search.Result at the ops boundary.
type SearchHit struct {
	ID     string
	Bytes  []byte
	Fields map[string]any
}

const defaultListLimit = 10

func resolveLimit(limit int, all bool) int {
	if all {
		return 0
	}
	if limit > 0 {
		return limit
	}
	return defaultListLimit
}

// ListSections enumerates every record id reachable under `scope`.
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
		out[i] = r.ID
	}
	return out, nil
}

// ScopeRecord is one record returned by GetScope.
type ScopeRecord struct {
	ID     string
	Bytes  []byte
	Fields map[string]any
}

// IsScopeAddress reports whether id is a scope-prefix id (e.g.
// `<file-relpath>` alone) rather than a fully-qualified single-record
// id (`<file-relpath>.<bracket-key>`).
//
// Per F10: a scope-prefix id is one whose ResolveID either:
//
//  1. Succeeds with BracketKey == "" (the id is exactly the file-relpath
//     of some mount); OR
//  2. Fails with ErrBadID (the id matches a mount file-relpath but lacks
//     a bracket-key); OR
//  3. Fails with ErrIDDoesNotMatchAnyDB but extending it with one
//     synthetic bracket-key segment makes it parse.
func IsScopeAddress(path, id string) (bool, error) {
	if id == "" {
		return false, fmt.Errorf("%w: empty id", search.ErrInvalidScope)
	}
	parts := strings.Split(id, ".")
	if slices.Contains(parts, "") {
		return false, fmt.Errorf("%w: %q has empty segment", search.ErrInvalidScope, id)
	}
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return false, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	resolved, _, parseErr := resolver.ResolveID(id)
	if parseErr == nil {
		if resolved.BracketKey != "" {
			return false, nil
		}
		return true, nil
	}
	if errors.Is(parseErr, db.ErrBadID) {
		return true, nil
	}
	// Probe: try extending id with one synthetic bracket-key per
	// declared db. A successful extension means the original id is a
	// valid scope-prefix.
	const probeKey = "__ta_scope_probe__"
	for range resolution.Registry.DBs {
		extended := id + "." + probeKey
		if _, _, err := resolver.ResolveID(extended); err == nil {
			return true, nil
		}
	}
	return false, fmt.Errorf("%w: %v", search.ErrInvalidScope, parseErr)
}

// GetScope enumerates every record under a scope-prefix id.
func GetScope(path, id string, fields []string, limit int, all bool) ([]ScopeRecord, error) {
	results, err := search.Run(search.Query{
		Path:  path,
		Scope: id,
		Limit: resolveLimit(limit, all),
		All:   all,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ScopeRecord, len(results))
	for i, r := range results {
		out[i] = ScopeRecord{
			ID:     r.ID,
			Bytes:  r.Bytes,
			Fields: filterFields(r.Fields, fields),
		}
	}
	return out, nil
}

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

// Search executes a ta `search` query.
func Search(path, scope, typeName string, match map[string]any, queryRegex, field string, limit int, all bool) ([]SearchHit, error) {
	walkerLimit := limit
	walkerAll := all
	if typeName != "" {
		walkerLimit = 0
		walkerAll = true
	}
	q := search.Query{
		Path:  path,
		Scope: scope,
		Match: match,
		Field: field,
		Limit: resolveLimit(walkerLimit, walkerAll),
		All:   walkerAll,
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
	hits := make([]SearchHit, 0, len(results))
	if typeName != "" {
		// Validate db-qualified shape and cross-check via the index.
		dot := strings.Index(typeName, ".")
		if dot < 0 {
			return nil, fmt.Errorf("%w: got %q (search --type must be db-qualified)", ErrTypeNotQualified, typeName)
		}
		bareType := typeName[dot+1:]
		idx, _ := tryLoadIndex(path)
		for _, r := range results {
			if idx == nil {
				// No index → cannot type-filter; surface no hits
				// rather than silently passing every result.
				continue
			}
			entry, ok := idx.Get(r.ID)
			if !ok {
				// Not indexed → cannot type-filter; skip silently.
				continue
			}
			if entry.Type != bareType {
				continue
			}
			hits = append(hits, SearchHit{ID: r.ID, Bytes: r.Bytes, Fields: r.Fields})
		}
		if !all && limit > 0 && len(hits) > limit {
			hits = hits[:limit]
		} else if !all && limit <= 0 && len(hits) > defaultListLimit {
			hits = hits[:defaultListLimit]
		}
		return hits, nil
	}
	for _, r := range results {
		hits = append(hits, SearchHit{ID: r.ID, Bytes: r.Bytes, Fields: r.Fields})
	}
	return hits, nil
}
