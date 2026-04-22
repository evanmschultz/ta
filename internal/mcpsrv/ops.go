package mcpsrv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// Update updates an existing record. Fails with ErrFileNotFound when
// the backing file does not exist.
func Update(path, section string, data map[string]any) (string, []string, error) {
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
	emitted, err := backend.Emit(backendSection, record.Record(data))
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

// SearchHit mirrors search.Result at the mcpsrv boundary so callers
// (MCP handler, CLI subcommand) can depend on mcpsrv alone.
type SearchHit struct {
	Section string
	Bytes   []byte
	Fields  map[string]any
}

// Search executes a ta `search` query. scope, match, queryRegex, and
// field are optional. queryRegex is compiled with regexp.Compile — pass
// "" to skip the regex pass. Mirrors V2-PLAN §3.7.
func Search(path, scope string, match map[string]any, queryRegex, field string) ([]SearchHit, error) {
	q := search.Query{
		Path:  path,
		Scope: scope,
		Match: match,
		Field: field,
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
