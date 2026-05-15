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

// resolveIDForCallerType is the type-aware resolver entry point used
// by Create / Update / Delete. When typeName is db-qualified (e.g.
// `agents.agent`) the resolver is constrained to the named db via
// ResolveIDInDB — preventing an alphabetically-earlier db with a
// looser mount shape from swallowing the id under plain ResolveID
// (F29). When typeName is empty (Get and other read paths that do
// not require --type) or malformed (no dot), falls back to plain
// ResolveID and lets resolveTypeForID surface ErrTypeNotQualified
// downstream so the caller sees a single unified error path.
//
// Note the asymmetry across ops endpoints: Create requires --type,
// Update and Delete accept it as a cross-check but do not require
// it, and Get does not pass --type at all. F29's tightening only
// applies when --type was actually supplied; the legacy "first db
// whose mount accepts the id" semantics survive for the read /
// type-less mutation paths so existing call sites keep working.
func resolveIDForCallerType(resolver *db.Resolver, id, typeName string) (db.Resolved, schema.DB, error) {
	if typeName == "" {
		return resolver.ResolveID(id)
	}
	dbPart, _, ok := strings.Cut(typeName, ".")
	if !ok || dbPart == "" {
		// Not db-qualified or empty db part — let plain ResolveID
		// run; resolveTypeForID downstream surfaces the canonical
		// ErrTypeNotQualified for this case.
		return resolver.ResolveID(id)
	}
	return resolver.ResolveIDInDB(id, dbPart)
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
	// F38d-2.14: when typeName is empty (the normal Get path — callers
	// are not required to supply --type on reads), consult the index to
	// pick the correct db before resolution. Without this hint an
	// alphabetically-earlier db with a looser mount shape (e.g.
	// claude_agents glob `agents/*/*.md`) can swallow an id that
	// actually belongs to a later db (e.g. plans single-file
	// `.ta/cascade/plans.toml`). When typeName is non-empty the caller
	// has already named the db; delegate to resolveIDForCallerType so
	// the F29 constraint fires as before.
	var resolved db.Resolved
	var dbDecl schema.DB
	if typeName == "" {
		resolved, dbDecl, err = resolveIDWithIndexHint(resolver, resolution.Registry, path, id)
	} else {
		resolved, dbDecl, err = resolveIDForCallerType(resolver, id, typeName)
	}
	if err != nil {
		return GetResult{}, err
	}
	bareType, err := resolveTypeForID(resolved, typeName, false, path, declaredTypeNames(dbDecl))
	if err != nil {
		return GetResult{}, err
	}
	// F38d-2.14 + F29: use resolved.FilePath directly. Going back
	// through resolver.ResolveRead would re-run unconstrained ResolveID
	// and silently switch to a different db's file when two dbs accept
	// the same id with different mount prefixes.
	filePath := resolved.FilePath
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return GetResult{}, fmt.Errorf("%w: %s", ErrFileNotFound, filePath)
		}
		return GetResult{}, fmt.Errorf("stat %s: %w", filePath, err)
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return GetResult{}, fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl, resolved)
	if err != nil {
		return GetResult{}, err
	}
	backendSection := backendSectionPath(dbDecl, resolved, bareType)
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
	// F38d-2.14: same index-hint disambiguation as Get. See Get comment.
	var resolved db.Resolved
	var dbDecl schema.DB
	if typeName == "" {
		resolved, dbDecl, err = resolveIDWithIndexHint(resolver, resolution.Registry, path, id)
	} else {
		resolved, dbDecl, err = resolveIDForCallerType(resolver, id, typeName)
	}
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
	// F38d-2.14 + F29: use resolved.FilePath directly (see Get comment).
	filePath := resolved.FilePath
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return GetResult{}, typeSt, fmt.Errorf("%w: %s", ErrFileNotFound, filePath)
		}
		return GetResult{}, typeSt, fmt.Errorf("stat %s: %w", filePath, err)
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return GetResult{}, typeSt, fmt.Errorf("read %s: %w", filePath, err)
	}
	backend, err := buildBackend(dbDecl, resolved)
	if err != nil {
		return GetResult{}, typeSt, err
	}
	backendSection := backendSectionPath(dbDecl, resolved, bareType)
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

// CreateOptions controls optional behavior of CreateWithOptions. NoSpawn
// suppresses auto_spawn rules declared on the target type (F23). The
// MCP `create` tool surfaces this as `no_spawn=true`; the CLI surfaces
// it as `--no-spawn`.
type CreateOptions struct {
	// NoSpawn, when true, skips the [<db>.<type>.auto_spawn] rule on
	// the target type — the parent record is created with no children.
	// Default false (auto_spawn fires when the type declares one).
	NoSpawn bool
}

// Create creates a new record. typeName is REQUIRED and must be
// db-qualified (`<db>.<type>`). Legacy 3-return shim over
// CreateWithOptions; preserves the auto_spawn-fires default per F23.
func Create(path, id, typeName string, data map[string]any) (string, []string, error) {
	return CreateWithOptions(path, id, typeName, data, CreateOptions{})
}

// CreateWithOptions creates a new record and, when the target type
// declares an [<db>.<type>.auto_spawn] block (F23), spawns the
// declared children atomically-on-validation, sequentially-on-disk-
// write. Per F23's locked semantics:
//
//  1. Pre-validate every child payload (interpolate templates, merge
//     spec.fields, run resolution.Registry.Validate against the
//     target type). On any validation failure: no disk writes occur
//     and the validation error surfaces directly. The parent record
//     is also pre-validated up front, same as the legacy path.
//  2. Sequential writes: parent first, then each child in declaration
//     order. Each write goes through the existing
//     toml.WriteAtomic + index.Save pipeline. A mid-pass write
//     failure (after at least one prior child has landed) wraps the
//     underlying error with ErrSpawnPartialWrite and lists landed /
//     missing ids so an operator can reconcile manually.
//  3. opts.NoSpawn=true skips the spawn pass entirely; only the parent
//     record is written.
//
// The pre-create probe still fires on the parent and on every spawn
// child; an existing record at any spawn id surfaces ErrRecordExists
// before any disk mutation occurs.
func CreateWithOptions(path, id, typeName string, data map[string]any, opts CreateOptions) (string, []string, error) {
	if typeName == "" {
		return "", nil, fmt.Errorf("%w: create requires --type (db-qualified, e.g. `plans.task`)", ErrTypeMismatch)
	}
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	// F29: Create requires --type and is db-qualified, so constrain
	// the mount-iteration loop to the caller's named db. Prevents an
	// alphabetically-earlier db with a looser mount shape (e.g.
	// `claude_agents/*.md`) from swallowing a 2-segment id when the
	// caller's intent was a different db (`agents/*/*.md`).
	resolved, dbDecl, err := resolveIDForCallerType(resolver, id, typeName)
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

	// F23 atomicity rule: pre-validate every spawn child BEFORE any
	// disk write. We can't pre-snapshot the per-write buffer (sequential
	// writes append to the same file in single-file dbs), so the
	// pre-validate pass produces "intents" — interpolated id + payload
	// + resolved view — and the actual planRecordWrite + write happens
	// one record at a time below, snapshotting fresh state each step.
	parentType := dbDecl.Types[bareType]
	var spawnIntents []spawnIntent
	if !opts.NoSpawn && len(parentType.AutoSpawn) > 0 {
		spawnIntents, err = preValidateAutoSpawn(resolver, resolution.Registry, id, parentType.AutoSpawn)
		if err != nil {
			return "", nil, err
		}
	}

	// Pre-create probe on the parent. Runs after spawn pre-validation
	// so any spec-level error fires before this read; runs before the
	// parent write so a parent-id collision aborts cleanly.
	parentPlan, err := planRecordWrite(dbDecl, resolved, id, bareType, data)
	if err != nil {
		return "", nil, err
	}

	// Pre-create probe on every spawn child. Each probe reads the
	// CURRENT file state (no writes have happened yet) so an existing
	// record at any spawn id surfaces ErrRecordExists before we touch
	// disk. We also probe the parent file separately above.
	for i, intent := range spawnIntents {
		if err := probeSpawnChild(intent); err != nil {
			return "", nil, fmt.Errorf("auto_spawn[%d]: %w", i, err)
		}
	}

	// Phase 2: sequential writes. Parent first.
	if err := executeRecordWrite(path, resolved, parentPlan); err != nil {
		return "", nil, err
	}

	// Phase 3: spawn children. Each iteration plans (re-snapshots the
	// file) and writes; sequential best-effort. On mid-pass failure
	// after the parent landed, wrap with ErrSpawnPartialWrite listing
	// landed and missing ids.
	landed := []string{id}
	for i, intent := range spawnIntents {
		childPlan, err := planRecordWrite(intent.dbDecl, intent.resolved,
			intent.id, intent.bareType, intent.data)
		if err != nil {
			missing := collectIntentIDs(spawnIntents[i:])
			return parentPlan.filePath, resolution.Sources, fmt.Errorf(
				"%w: landed=[%s] missing=[%s]: %v",
				ErrSpawnPartialWrite,
				strings.Join(landed, ","),
				strings.Join(missing, ","),
				err,
			)
		}
		if err := executeRecordWrite(path, intent.resolved, childPlan); err != nil {
			missing := collectIntentIDs(spawnIntents[i:])
			return parentPlan.filePath, resolution.Sources, fmt.Errorf(
				"%w: landed=[%s] missing=[%s]: %v",
				ErrSpawnPartialWrite,
				strings.Join(landed, ","),
				strings.Join(missing, ","),
				err,
			)
		}
		landed = append(landed, intent.id)
	}
	return parentPlan.filePath, resolution.Sources, nil
}

// spawnIntent is the pre-validated, pre-resolved representation of one
// auto_spawn child. preValidateAutoSpawn produces these in declaration
// order; the per-record planRecordWrite + executeRecordWrite happens
// later, one at a time, against fresh file state per F23's
// pre-validate-all + sequential-write atomicity rule.
type spawnIntent struct {
	id       string
	bareType string
	dbDecl   schema.DB
	resolved db.Resolved
	data     map[string]any
}

// collectIntentIDs returns the ids of every intent in `rest` for use
// in an ErrSpawnPartialWrite missing-list message.
func collectIntentIDs(rest []spawnIntent) []string {
	out := make([]string, len(rest))
	for i, in := range rest {
		out[i] = in.id
	}
	return out
}

// probeSpawnChild runs the pre-create existence probe for one spawn
// intent: build a backend, read the file (or empty when absent), and
// surface ErrRecordExists if the bracket is already present. Runs
// BEFORE any write so the buffer reflects on-disk state, satisfying
// F23's pre-validate-all atomicity rule for the existence check.
func probeSpawnChild(intent spawnIntent) error {
	backend, err := buildBackend(intent.dbDecl, intent.resolved)
	if err != nil {
		return err
	}
	buf, err := readFileIfExists(intent.resolved.FilePath)
	if err != nil {
		return err
	}
	backendSection := backendSectionPath(intent.dbDecl, intent.resolved, intent.bareType)
	if _, exists, err := backend.Find(buf, backendSection); err != nil {
		return fmt.Errorf("pre-create probe %q: %w", intent.id, err)
	} else if exists {
		return fmt.Errorf("%w: %q", ErrRecordExists, intent.id)
	}
	return nil
}

// recordWritePlan captures everything needed to write one record after
// validation has passed: resolver-resolved view, target file path,
// backend, pre-emitted bytes, and the bare type name for the index.
// Used by CreateWithOptions to separate the validate-everything pass
// from the write-everything pass per F23.
type recordWritePlan struct {
	id             string
	bareType       string
	dbDecl         schema.DB
	resolved       db.Resolved
	filePath       string
	backend        record.Backend
	backendSection string
	priorBuf       []byte
	emittedBytes   []byte
}

// planRecordWrite runs every read-side step Create needs before the
// first byte hits disk: resolve the write path, build a backend,
// snapshot the existing file, run the pre-create probe, and emit the
// new record bytes. Returns a recordWritePlan ready to hand to
// executeRecordWrite. Validation against the registry is the caller's
// responsibility — done before this helper runs.
func planRecordWrite(
	dbDecl schema.DB,
	resolved db.Resolved,
	id, bareType string,
	data map[string]any,
) (recordWritePlan, error) {
	// F29: use resolved.FilePath directly. Going back through
	// resolver.ResolveWrite would re-run unconstrained ResolveID and
	// silently switch to a different db's file when two dbs accept
	// the same id with different mount prefixes.
	filePath := resolved.FilePath
	backend, err := buildBackend(dbDecl, resolved)
	if err != nil {
		return recordWritePlan{}, err
	}
	buf, err := readFileIfExists(filePath)
	if err != nil {
		return recordWritePlan{}, err
	}
	backendSection := backendSectionPath(dbDecl, resolved, bareType)
	if _, exists, err := backend.Find(buf, backendSection); err != nil {
		return recordWritePlan{}, fmt.Errorf("pre-create probe %q: %w", id, err)
	} else if exists {
		return recordWritePlan{}, fmt.Errorf("%w: %q", ErrRecordExists, id)
	}
	emitted, err := backend.Emit(backendSection, record.Record(data))
	if err != nil {
		return recordWritePlan{}, fmt.Errorf("emit %q: %w", id, err)
	}
	return recordWritePlan{
		id:             id,
		bareType:       bareType,
		dbDecl:         dbDecl,
		resolved:       resolved,
		filePath:       filePath,
		backend:        backend,
		backendSection: backendSection,
		priorBuf:       buf,
		emittedBytes:   emitted,
	}, nil
}

// executeRecordWrite splices the emitted bytes into priorBuf, mkdirs
// the parent dir, atomic-writes the file, and writes the index entry.
// Mirrors the disk-write tail of legacy Create on a per-record basis.
// The plan-resolved arg is named so callers (parent + each spawn
// child) can pass the right Resolved without duplicating fields.
func executeRecordWrite(projectPath string, resolved db.Resolved, plan recordWritePlan) error {
	newBuf, err := plan.backend.Splice(plan.priorBuf, plan.backendSection, plan.emittedBytes)
	if err != nil {
		return fmt.Errorf("splice %q: %w", plan.id, err)
	}
	dir := filepath.Dir(plan.filePath)
	dirCreated := false
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		dirCreated = true
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := toml.WriteAtomic(plan.filePath, newBuf); err != nil {
		if dirCreated {
			if entries, lstErr := os.ReadDir(dir); lstErr == nil && len(entries) == 0 {
				_ = os.Remove(dir)
			}
		}
		return err
	}
	if err := writeIndexEntry(projectPath, resolved, plan.bareType); err != nil {
		return err
	}
	return nil
}

// preValidateAutoSpawn runs interpolation, resolution, and Validate
// for every spec in declaration order. Returns one spawnIntent per
// spec — the actual planRecordWrite + executeRecordWrite happens
// later, sequentially, with fresh file state per record. Per F23's
// pre-validate-all + sequential-write atomicity rule.
func preValidateAutoSpawn(
	resolver *db.Resolver,
	reg schema.Registry,
	parentID string,
	specs []schema.SpawnSpec,
) ([]spawnIntent, error) {
	intents := make([]spawnIntent, 0, len(specs))
	for i, spec := range specs {
		childID, err := interpolateSpawnString(spec.IDTemplate, parentID, i+1)
		if err != nil {
			return nil, fmt.Errorf("auto_spawn[%d]: id_template: %w", i, err)
		}
		childData, err := interpolateSpawnFields(spec.Fields, parentID, i+1)
		if err != nil {
			return nil, fmt.Errorf("auto_spawn[%d]: fields: %w", i, err)
		}
		childResolved, childDBDecl, err := resolver.ResolveID(childID)
		if err != nil {
			return nil, fmt.Errorf("auto_spawn[%d]: resolve %q: %w", i, childID, err)
		}
		dot := strings.Index(spec.Type, ".")
		if dot < 0 {
			return nil, fmt.Errorf(
				"auto_spawn[%d]: spec.type %q malformed (load-time validation should have caught this)",
				i, spec.Type,
			)
		}
		specDB := spec.Type[:dot]
		bareTargetType := spec.Type[dot+1:]
		if childResolved.DBName != specDB {
			return nil, fmt.Errorf(
				"auto_spawn[%d]: id_template %q resolves to db %q but spec.type targets db %q",
				i, spec.IDTemplate, childResolved.DBName, specDB,
			)
		}
		if err := reg.Validate(specDB+"."+bareTargetType, childData); err != nil {
			return nil, fmt.Errorf("auto_spawn[%d]: %w", i, err)
		}
		intents = append(intents, spawnIntent{
			id:       childID,
			bareType: bareTargetType,
			dbDecl:   childDBDecl,
			resolved: childResolved,
			data:     childData,
		})
	}
	// Reject intra-spec id collisions BEFORE any disk write so the
	// atomic-on-validation guarantee holds. Without this scan two specs
	// producing the same interpolated id would each pass the per-spec
	// existence probe (against pre-write state) and then collide on the
	// second write — leaving parent + child-1 on disk.
	seen := make(map[string]int, len(intents))
	for i, in := range intents {
		if prev, dup := seen[in.id]; dup {
			return nil, fmt.Errorf(
				"auto_spawn: specs %d and %d produce the same id %q (intra-spec collision)",
				prev, i, in.id,
			)
		}
		seen[in.id] = i
	}
	return intents, nil
}

// interpolateSpawnString applies the F23 v1 token rule: `{parent_id}` →
// parentID; `{index}` → 1-based index as decimal. Other tokens are
// rejected (they should already be caught at schema-load by
// validateSpawnTemplateTokens; this is a defense-in-depth check).
// Empty input returns empty output — useful for static field values
// like `notes = ""`.
func interpolateSpawnString(s, parentID string, idx int) (string, error) {
	if s == "" {
		return "", nil
	}
	out := strings.ReplaceAll(s, "{parent_id}", parentID)
	out = strings.ReplaceAll(out, "{index}", fmt.Sprintf("%d", idx))
	// Defense-in-depth: any remaining `{...}` is an unknown token that
	// somehow slipped past load.
	if strings.Contains(out, "{") {
		return "", fmt.Errorf("template %q has unknown token after interpolation", s)
	}
	return out, nil
}

// interpolateSpawnFields walks the static fields map and applies token
// interpolation to every string-typed value. Non-string values pass
// through unchanged. Returns a fresh map so subsequent mutations on
// the returned data cannot leak into the schema-cached spec.
func interpolateSpawnFields(in map[string]any, parentID string, idx int) (map[string]any, error) {
	out := make(map[string]any, len(in))
	for k, v := range in {
		s, isStr := v.(string)
		if !isStr {
			out[k] = v
			continue
		}
		interp, err := interpolateSpawnString(s, parentID, idx)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		out[k] = interp
	}
	return out, nil
}

// Update applies a PATCH-style partial overlay to an existing record.
func Update(path, id, typeName string, data map[string]any) (string, []string, error) {
	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve schema for %s: %w", path, err)
	}
	resolver := db.NewResolver(path, resolution.Registry)
	// F29+F38d-2.14: when the caller supplied --type, constrain
	// mount-iteration to the named db. When typeName is empty, use the
	// index hint to pick the correct db before resolution (same
	// disambiguation as Get — see Get comment for the failure mode).
	var resolved db.Resolved
	var dbDecl schema.DB
	if typeName == "" {
		resolved, dbDecl, err = resolveIDWithIndexHint(resolver, resolution.Registry, path, id)
	} else {
		resolved, dbDecl, err = resolveIDForCallerType(resolver, id, typeName)
	}
	if err != nil {
		return "", nil, err
	}
	// F29: use resolved.FilePath directly. Going back through
	// resolver.ResolveRead would re-run unconstrained ResolveID and
	// silently switch to a different db's file when two dbs accept
	// the same id with different mount prefixes. File-existence check
	// stays here (ahead of resolveTypeForID) so a missing backing file
	// surfaces as ErrFileNotFound rather than as ErrIndexMissing /
	// ErrTypeUnresolved.
	filePath := resolved.FilePath
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("%w: %s", ErrFileNotFound, filePath)
		}
		return "", nil, fmt.Errorf("stat %s: %w", filePath, err)
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
	backendSection := backendSectionPath(dbDecl, resolved, bareType)

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
//     and converts an interactive confirm `true` into Force=true;
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

	// F38d-2.14b: when typeName is empty (the normal MCP / CLI delete
	// path — callers are not required to supply --type), consult the
	// index to pick the correct db BEFORE handing off to ResolveDelete.
	// Without this hint, ResolveDelete's Path 1 calls unconstrained
	// ResolveID which an alphabetically-earlier db with a looser mount
	// shape (e.g. claude_agents glob `agents/*/*.md`) can swallow,
	// returning BracketKey=="" and falling through to Path 2's instance
	// scan, which then surfaces ErrBadID "has no bracket-key and matches
	// no concrete file" — the canonical F38d-2.14 failure mode. The hint
	// must return a non-empty BracketKey (i.e. a record-level resolution
	// against the correct db) to short-circuit; an empty-BracketKey hit
	// (e.g. file-as-record) or a hint miss falls back to ResolveDelete so
	// LevelFile / LevelGlobRoot semantics still flow through the
	// existing resolver path.
	//
	// F38d-2.14b extension: when typeName is db-qualified (the MCP delete
	// tool exposes a `type` field per item, and CLI users pass --type as
	// the safety-first convention), short-circuit via ResolveIDInDB
	// against the caller's named db FIRST — same intent as the empty-
	// typeName index-hint path, but using the caller's explicit type as
	// the constraint instead of the index. Without this, ResolveDelete's
	// Path 1 hits unconstrained ResolveID with the same canonical F38d-
	// 2.14 failure mode for typeName-supplying callers (the original
	// F38d-2.14b landed only the typeName=="" branch). When ResolveIDInDB
	// returns a non-empty BracketKey we're at LevelRecord; when it misses
	// (mount-shape mismatch — caller named the wrong db, or id is a bare
	// file-relpath the named db's section-mode mount cannot accept), fall
	// back to ResolveDelete so LevelFile / LevelGlobRoot semantics and
	// the F29 cross-check below still fire. The fall-back error path also
	// surfaces ErrIDDoesNotMatchAnyDB when the caller's --type names a db
	// that does not accept the id — a clear type-mismatch signal.
	var resolved db.Resolved
	var dbDecl schema.DB
	var level db.DeleteLevel
	constrainedByTypeHint := false
	if typeName == "" {
		if hintRes, hintDB, hintErr := resolveIDWithIndexHint(resolver, resolution.Registry, path, id); hintErr == nil && hintRes.BracketKey != "" {
			resolved = hintRes
			dbDecl = hintDB
			level = db.LevelRecord
		} else {
			resolved, dbDecl, level, err = resolver.ResolveDelete(id)
			if err != nil {
				if errors.Is(err, db.ErrUnscopedGlobDelete) {
					return DeleteResult{Sources: resolution.Sources, Level: db.LevelGlobRoot},
						fmt.Errorf("%w: %v", ErrUnscopedGlobDelete, err)
				}
				return DeleteResult{Sources: resolution.Sources}, err
			}
		}
	} else {
		if dbPart, _, ok := strings.Cut(typeName, "."); ok && dbPart != "" {
			if hintRes, hintDB, hintErr := resolver.ResolveIDInDB(id, dbPart); hintErr == nil && hintRes.BracketKey != "" {
				resolved = hintRes
				dbDecl = hintDB
				level = db.LevelRecord
				constrainedByTypeHint = true
			}
		}
		if !constrainedByTypeHint {
			resolved, dbDecl, level, err = resolver.ResolveDelete(id)
			if err != nil {
				if errors.Is(err, db.ErrUnscopedGlobDelete) {
					return DeleteResult{Sources: resolution.Sources, Level: db.LevelGlobRoot},
						fmt.Errorf("%w: %v", ErrUnscopedGlobDelete, err)
				}
				return DeleteResult{Sources: resolution.Sources}, err
			}
		}
	}

	// F29: when --type is supplied for a record-level delete, re-run
	// resolution constrained to the named db so a 2-segment id does
	// not silently fall through to an alphabetically-earlier db with
	// a looser mount shape. File-level delete operates on bare
	// file-relpaths and does not take --type, so the constraint only
	// applies on the record branch.
	//
	// F38d-2.14b extension: skip when we already constrained via the
	// up-front ResolveIDInDB short-circuit above. The check is otherwise
	// idempotent (it would re-run the same call with the same result),
	// but skipping makes the trace explicit: the type-hint either ran
	// up front (constrainedByTypeHint=true) OR runs here after a
	// fallback ResolveDelete that may have landed on the wrong db.
	if level == db.LevelRecord && typeName != "" && !constrainedByTypeHint {
		if dbPart, _, ok := strings.Cut(typeName, "."); ok && dbPart != "" {
			constrained, constrainedDB, cerr := resolver.ResolveIDInDB(id, dbPart)
			if cerr != nil {
				return DeleteResult{Sources: resolution.Sources, Level: db.LevelRecord}, cerr
			}
			resolved = constrained
			dbDecl = constrainedDB
		}
	}

	switch level {
	case db.LevelRecord:
		return deleteRecord(path, resolution.Sources, dbDecl, resolved, id, typeName)
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
			"ops: DeleteWithOptions: unexpected level %d for id %q", level, id,
		)
	}
}

// deleteRecord is the record-level delete path. It mirrors the pre-F19
// flow: validate type cross-check (when typeName is supplied), splice
// the record bytes out of the backing file, prune the index entry,
// then return the post-delete count of records remaining in the same
// file (file-scoped, per F20 lock).
func deleteRecord(path string, sources []string, dbDecl schema.DB, resolved db.Resolved, id, typeName string) (DeleteResult, error) {
	bareType, err := resolveTypeForID(resolved, typeName, false, path, declaredTypeNames(dbDecl))
	if err != nil {
		return DeleteResult{Sources: sources, Level: db.LevelRecord}, err
	}
	// F29: use resolved.FilePath directly. Going back through
	// resolver.ResolveRead would re-run unconstrained ResolveID and
	// silently switch to a different db's file when two dbs accept
	// the same id with different mount prefixes. The os.ReadFile
	// below catches a missing backing file and maps to ErrFileNotFound.
	filePath := resolved.FilePath
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
	backendSection := backendSectionPath(dbDecl, resolved, bareType)
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
//
// Per F31: when the resolved db is a file-as-record db, an empty
// BracketKey signals a complete record id (the file IS the record),
// NOT a scope-prefix. R1 audit finding — without this branch a
// file-as-record id would route through GetScope instead of Get.
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
	resolved, dbDecl, parseErr := resolver.ResolveID(id)
	if parseErr == nil {
		if resolved.BracketKey != "" {
			return false, nil
		}
		// F31: a successful resolve with empty BracketKey on a
		// file-as-record db is a complete record id, not a scope
		// prefix.
		if schema.DBHasFileAsRecord(dbDecl) {
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
