package search

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"sort"
	"strings"

	tomlv2 "github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/backend/md"
	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// Query is the input to Run. Only Path is strictly required; the other
// fields narrow the search.
//
// Semantics:
//   - Scope is either empty (whole project) or any id prefix
//     (e.g. `plans` to limit traversal to one file, `plans.todo-` to
//     limit to ids beginning `plans.todo-`). A trailing "-*" or "*"
//     wildcard on the prefix is tolerated as a no-op.
//   - Match pairs AND-combine; every pair must match exactly. String/enum
//     compare via Go ==; numbers compare numerically (int vs float
//     promoted). Array/table match → ErrUnscalarMatch.
//   - Query is applied AFTER Match on records that passed Match. When
//     Field == "" the regex is matched against every string-typed field;
//     when Field is set, only that one declared string field is scanned.
//     A hit is any FindIndex match.
//   - Limit caps the returned hit count; 0 means "no cap". When Limit > 0
//     and All is false, Run early-exits after each file's results are
//     appended once len(out) >= Limit — O(until first cap-cross) rather
//     than O(all records). Ignored when All is true.
//   - All == true returns every hit (ignores Limit). Adapters (CLI cobra
//     mutex, MCP handler guard) enforce the UX-level "pass limit OR all,
//     not both" rule; this type stays permissive at the endpoint layer
//     so library callers see predictable precedence (All wins). See
//     docs/PLAN.md §6a.1 + §3.2 + §3.7 + §12.17.5 [A2.1]+[A2.2].
type Query struct {
	Path  string
	Scope string
	Match map[string]any
	Query *regexp.Regexp
	Field string
	Limit int
	All   bool
}

// Result is one hit. ID is the full record id; Bytes is the record's
// on-disk byte range (what `get` would return); Fields is the decoded
// field map for callers that want structured access.
type Result struct {
	ID     string
	Bytes  []byte
	Fields map[string]any
}

// Run executes q and returns hits in source order across files. Files
// are visited in stable lexical order so results are deterministic
// across runs.
func Run(q Query) ([]Result, error) {
	if q.Path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrInvalidScope)
	}
	resolution, err := resolve(q.Path)
	if err != nil {
		return nil, err
	}
	plan, err := parseScope(resolution.Registry, q.Path, q.Scope)
	if err != nil {
		return nil, err
	}
	// Per F10 the search no longer derives a type from the scope; we
	// run validateScopeNames unconditionally to catch pure typos, AND
	// validate that each declared type in scope can satisfy any
	// requested non-scalar Match (a field that is array/table on
	// every type in scope is a typed-contract violation).
	if err := validateScopeNames(resolution.Registry, plan, q); err != nil {
		return nil, err
	}
	if err := validateScopeMatchSemantics(resolution.Registry, plan, q); err != nil {
		return nil, err
	}

	resolver := db.NewResolver(q.Path, resolution.Registry)

	// F38d-2.16 post-walk type filter: when the scope was
	// `<db>.<type>`, load the project index once so searchFile can
	// drop hits whose canonical id's indexed type differs from the
	// requested type. A missing or unreadable index collapses the
	// result set to empty rather than silently passing every record —
	// mirrors ops.Search's type-filter precedent (no index → no
	// typed-narrowing possible).
	var idx *index.Index
	if plan.typeFilter != "" {
		idx, err = loadIndexForTypeFilter(q.Path)
		if err != nil {
			return nil, err
		}
	}

	var out []Result
	for _, dbName := range plan.dbOrder {
		dbDecl := resolution.Registry.DBs[dbName]
		instances, err := resolver.Instances(dbName)
		if err != nil {
			return nil, err
		}
		for _, inst := range instances {
			if plan.fileRelPath != "" && inst.Slug != plan.fileRelPath {
				continue
			}
			if _, err := os.Stat(inst.FilePath); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("stat %s: %w", inst.FilePath, err)
			}
			results, err := searchFile(dbDecl, inst, plan, q)
			if err != nil {
				return nil, err
			}
			if plan.typeFilter != "" {
				results = filterByIndexedType(results, idx, plan.typeFilter)
			}
			out = append(out, results...)
			// Endpoint-enforced cap with file-boundary early-exit per
			// docs/PLAN.md §3.2 / §3.7 amendment. All=true bypasses the
			// cap; Limit<=0 means "no cap" (callers that want the default
			// substitute it at the adapter/ops layer before calling Run).
			if !q.All && q.Limit > 0 && len(out) >= q.Limit {
				return out[:q.Limit], nil
			}
		}
	}
	return out, nil
}

// loadIndexForTypeFilter mirrors ops.tryLoadIndex semantics for the
// search package: a missing index file is NOT an error — return nil so
// filterByIndexedType collapses the result set to empty. Other I/O or
// parse errors propagate.
func loadIndexForTypeFilter(projectPath string) (*index.Index, error) {
	idx, err := index.Load(projectPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("search: load index for type filter: %w", err)
	}
	return idx, nil
}

// filterByIndexedType drops every Result whose canonical id maps to
// an index entry with a different Type. When the index is nil (absent
// or unreadable) the filter returns an empty slice — callers that
// requested a type-scoped search MUST have an index, mirroring
// ops.Search's type-filter precedent.
func filterByIndexedType(in []Result, idx *index.Index, typeName string) []Result {
	if idx == nil {
		return nil
	}
	out := in[:0]
	for _, r := range in {
		entry, ok := idx.Get(r.ID)
		if !ok {
			continue
		}
		if entry.Type != typeName {
			continue
		}
		out = append(out, r)
	}
	return out
}

// resolve is a local mirror of ops.ResolveProject so this package
// does not import ops. Post-V2-PLAN §12.11 the resolver reads the
// single project-local .ta/schema.toml directly — no sentinel trick.
func resolve(projectPath string) (config.Resolution, error) {
	return config.Resolve(projectPath)
}

// searchPlan carries the parsed Query.Scope as a list of dbs to visit
// plus the optional file-relpath and bracket-key-prefix narrowing
// filters. Per F10 the type segment is no longer part of the user-
// facing id, so searchPlan no longer carries a typeName field; the
// caller (ops.Search) supplies type filtering via the typeName arg
// and consults the index for authoritative type info.
//
// F38d-2.16 special-case: when Scope is `<dbname>.<typename>` AND no
// mount accepts that file-relpath shape, parseScope sets typeFilter
// to the bare type name. Run applies a post-walk index-driven filter:
// any hit whose canonical id maps to a different type in the on-disk
// index is dropped. This brings the cascade-style "list every record
// of type X" query (used by `mcp__ta__list_sections` and `search` at
// the dogfood layer) into the search grammar without reintroducing a
// type segment into the id.
type searchPlan struct {
	dbOrder     []string
	fileRelPath string // "" means "any file"
	idPrefix    string // "" means "any bracket-key prefix"
	typeFilter  string // F38d-2.16: bare type name; "" means "no filter"
}

// match is the per-mount scope-match candidate. parseScope's helper
// matchFixedScope builds these and consider() picks a winner via the
// longer-slug-wins tiebreaker.
//
// globOnly is true when every residual segment of the matched mount
// (after stripping the format extension) is the bare wildcard "*". A
// glob-only match has the structural property that ANY parts[i] in
// that position satisfies the segment — meaning the match success says
// nothing about whether parts[i] genuinely belongs in that file-relpath
// slot. F38d-2.17 uses this flag to break the shadowing tie when a
// glob-only mount silently swallows a 2-segment scope that should
// instead resolve as `<db>.<type>` (typeFilter) under a sibling db.
type match struct {
	dbName, slug, idPrefix string
	collection             bool
	globOnly               bool
}

// parseScope validates Scope against the registry under the Phase 9.2
// `<file-relpath>.<type>.<id-prefix>` grammar. Empty scope walks every
// db. Otherwise the scope is matched against every db's mounts:
//
//   - For non-collection mounts the file-relpath segment-count is
//     fixed (matches the mount's residual segments). Scope's first N
//     parts are the file-relpath; the rest is `<type>(.<id-prefix>)?`.
//   - For collection mounts the file-relpath length is variable.
//     The scope is split at the rightmost segment that names a
//     declared type; left of it is file-relpath, right is id-prefix.
//
// At least the file-relpath shape must match a known mount; the type
// segment (if present) must match a declared type. Matching is
// disk-independent — first-create scopes (the file does not yet exist)
// resolve identically to scopes whose file is on disk. The traversal
// phase in Run handles the empty-result-for-missing-file case.
func parseScope(reg schema.Registry, projectPath, scope string) (searchPlan, error) {
	_ = projectPath // disk-independent — kept in signature for future hooks.

	if scope == "" {
		names := make([]string, 0, len(reg.DBs))
		for n := range reg.DBs {
			names = append(names, n)
		}
		sort.Strings(names)
		return searchPlan{dbOrder: names}, nil
	}

	parts := strings.Split(scope, ".")
	for _, p := range parts {
		if p == "" {
			return searchPlan{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
		}
	}

	// Bare db-name short-circuit (F38d-2.10): a single-segment scope that
	// names a declared db means "walk every record in that db" regardless
	// of mount shape. The fall-through fixed-mount matcher mis-routes
	// glob-only mounts like `.claude/agents/*.md` because the trailing
	// `*` happily eats the db-name as a fake file-relpath (slug becomes
	// the db-name, which matches no real instance). Special-casing here
	// preserves single-file mount behavior (slug==db-name in those cases
	// anyway) and fixes file-record dbs whose slugs are file basenames.
	if len(parts) == 1 {
		if _, ok := reg.DBs[parts[0]]; ok {
			return searchPlan{dbOrder: []string{parts[0]}}, nil
		}
	}

	dbNames := make([]string, 0, len(reg.DBs))
	for n := range reg.DBs {
		dbNames = append(dbNames, n)
	}
	sort.Strings(dbNames)

	var best *match
	consider := func(cand match) {
		if best == nil {
			best = &cand
			return
		}
		// Tiebreaker: longer slug wins (more specific match);
		// non-collection beats collection when slug-length ties.
		if len(cand.slug) > len(best.slug) {
			best = &cand
			return
		}
		if len(cand.slug) == len(best.slug) && best.collection && !cand.collection {
			best = &cand
		}
	}

	for _, dbName := range dbNames {
		dbDecl := reg.DBs[dbName]
		for _, mount := range dbDecl.Paths {
			// Collection mounts are rejected at schema-load time per
			// F10; only fixed-shape (literal/glob) mounts reach here.
			if cand, ok := matchFixedScope(parts, dbName, dbDecl, mount); ok {
				cand.collection = false
				consider(cand)
			}
		}
	}

	// F38d-2.17 disambiguation: a 2-segment scope whose first part names
	// a declared db has two possible interpretations:
	//
	//   1. `<db>.<type>` — typeFilter (F38d-2.16): walk the db, then
	//      drop hits whose canonical id's indexed type differs.
	//   2. `<file-relpath>` — fixed-shape mount match: walk one file.
	//
	// Glob-only mounts (every residual segment is bare `*`, e.g.
	// `agents/*/*.md`) accept ANY parts[i] in those positions — their
	// `match` against `cascade.drop` proves nothing about whether
	// `cascade.drop` is actually a legitimate file-relpath for that db,
	// it only proves the shape accepts two arbitrary segments. When
	// best is glob-only AND parts[0] names a different (or even the
	// same) declared db, the structurally-stronger interpretation is
	// the typeFilter / ErrInvalidScope dispatch driven by parts[1].
	//
	// Rule, applied in this order:
	//   (a) parts[1] is a declared type on parts[0]: typeFilter wins,
	//       regardless of best. This is the deliberate semantic — the
	//       schema's declared types are an unambiguous signal that the
	//       caller meant `<db>.<type>` and not a file-relpath collision.
	//   (b) best is a non-glob-only match (at least one literal segment
	//       constrained the match): legitimate file-relpath wins.
	//   (c) parts[0] is a declared db: parts[1] is NOT a declared type
	//       (else (a) would have fired) AND any best is glob-only (else
	//       (b) would have fired). The 2-seg `<db>.<typo>` shape under
	//       a shadowing-glob schema must surface ErrInvalidScope rather
	//       than silently returning the phantom file-relpath match.
	//   (d) best != nil: glob-only file-relpath, no db-shape disambig
	//       available (e.g. `agents.go.builder` 3-seg legit glob-only).
	//   (e) Otherwise: ErrInvalidScope.
	if len(parts) == 2 {
		if dbDecl, ok := reg.DBs[parts[0]]; ok {
			if _, declared := dbDecl.Types[parts[1]]; declared {
				return searchPlan{
					dbOrder:    []string{parts[0]},
					typeFilter: parts[1],
				}, nil
			}
			if best == nil || best.globOnly {
				return searchPlan{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
			}
		}
	}
	// F38d-2.18 extends the F38d-2.17 disambig to 3+ segment scopes:
	// `<db>.<type>.<id-prefix...>` under a shadowing glob-only schema
	// must resolve as the typeFilter intent rather than be silently
	// swallowed as a phantom <file-relpath>.<extra-segs> match against a
	// glob-only mount. The trigger is `len(parts) >= 3` — CE2
	// demonstrated reproduction at depth 4 (`cascade.drop.id123.tail`
	// under cascadeShadowedByGlobMDSchema), so the same failure surfaces
	// at every depth above 2.
	//
	// Rules (parallel to the 2-seg block):
	//   (a) parts[0] is a declared db AND parts[1] is a declared type on
	//       that db AND best is glob-only (or nil): typeFilter wins,
	//       idPrefix = join(parts[2:], "."). trimGlob normalizes a
	//       trailing "-*"/"*" the same way matchFixedScope does.
	//   (b) parts[0] is a declared db AND parts[1] is NOT a declared type
	//       AND best is glob-only (or nil): pure-typo surface,
	//       ErrInvalidScope (matches the 2-seg typo branch).
	//   (c) Otherwise: fall through. A non-glob-only fixed-mount match
	//       (best != nil && !best.globOnly) is a legitimate file-relpath
	//       win at 3+ segments and must NOT be hijacked by the typeFilter
	//       path. A non-db parts[0] also falls through to the existing
	//       no-match / ErrInvalidScope tail.
	if len(parts) >= 3 {
		if dbDecl, ok := reg.DBs[parts[0]]; ok {
			if _, declared := dbDecl.Types[parts[1]]; declared {
				if best == nil || best.globOnly {
					return searchPlan{
						dbOrder:    []string{parts[0]},
						typeFilter: parts[1],
						idPrefix:   trimGlob(strings.Join(parts[2:], ".")),
					}, nil
				}
			} else if best == nil || best.globOnly {
				return searchPlan{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
			}
		}
	}
	if best == nil {
		return searchPlan{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	return searchPlan{
		dbOrder:     []string{best.dbName},
		fileRelPath: best.slug,
		idPrefix:    best.idPrefix,
	}, nil
}

// matchFixedScope tests scope parts against a fixed-shape mount under
// the F10 id grammar. The first len(residualSegs-after-ext-strip)
// parts must satisfy the mount's residual shape (`*` matches any
// non-empty seg; literals require equality). Everything after is
// treated as the bracket-key prefix.
//
// The returned match also reports whether every expected segment was
// a bare `*` (globOnly): such matches accept arbitrary parts[i] in
// those positions and are structurally weaker than any match that
// constrained at least one segment via a literal. F38d-2.17 uses this
// flag to keep a glob-only mount (e.g. `agents/*/*.md`) from
// shadowing a sibling db's `<db>.<type>` shape (e.g. `cascade.drop`).
func matchFixedScope(parts []string, dbName string, dbDecl schema.DB, mount string) (match, bool) {
	_, residualSegs := splitMountSegmentsForSearch(mount)
	expected := stripFormatExtForSearch(residualSegs, dbDecl.Format)
	if len(parts) < len(expected) {
		return match{}, false
	}
	globOnly := len(expected) > 0
	for i, seg := range expected {
		if seg == "*" {
			continue
		}
		if parts[i] != seg {
			return match{}, false
		}
		globOnly = false
	}
	slug := strings.Join(parts[:len(expected)], ".")
	rest := parts[len(expected):]
	var idPrefix string
	if len(rest) >= 1 {
		idPrefix = trimGlob(strings.Join(rest, "."))
	}
	return match{
		dbName:   dbName,
		slug:     slug,
		idPrefix: idPrefix,
		globOnly: globOnly,
	}, true
}

// splitMountSegmentsForSearch is a local mirror of db.splitMountSegments.
// Duplicating the helper avoids exporting db internals that should not
// leak across the search/db boundary.
func splitMountSegmentsForSearch(mount string) (string, []string) {
	if mount == "." {
		return "", []string{}
	}
	if strings.HasSuffix(mount, "/") {
		return mount, []string{}
	}
	segs := strings.Split(mount, "/")
	starIdx := -1
	for i, s := range segs {
		if s == "*" || strings.Contains(s, "*") {
			starIdx = i
			break
		}
	}
	if starIdx >= 0 {
		prefix := strings.Join(segs[:starIdx], "/")
		if prefix != "" {
			prefix += "/"
		}
		return prefix, segs[starIdx:]
	}
	if len(segs) == 1 {
		return "", segs
	}
	prefix := strings.Join(segs[:len(segs)-1], "/") + "/"
	return prefix, []string{segs[len(segs)-1]}
}

// stripFormatExtForSearch mirrors db.stripFormatExt.
func stripFormatExtForSearch(residualSegs []string, format schema.Format) []string {
	if len(residualSegs) == 0 {
		return residualSegs
	}
	last := residualSegs[len(residualSegs)-1]
	suffix := "." + string(format)
	if !strings.HasSuffix(last, suffix) {
		return residualSegs
	}
	out := make([]string, len(residualSegs))
	copy(out, residualSegs)
	out[len(out)-1] = strings.TrimSuffix(last, suffix)
	return out
}

// trimGlob strips a trailing "-*" or "*" on the id-prefix segment so
// the common "<db>.<type>.reference-*" form from §5.5.3 degrades to a
// plain prefix match on "reference-".
func trimGlob(s string) string {
	if trimmed, ok := strings.CutSuffix(s, "-*"); ok {
		return trimmed + "-"
	}
	return strings.TrimSuffix(s, "*")
}

// searchFile runs the query against one instance file.
//
// Per-instance singleFile selection (F11 literal-multi-path fix): the
// on-disk bracket form is determined by the mount that produced THIS
// instance, not by the db-wide `schema.IsSingleFileDB(dbDecl)` view.
// A db declared with `paths = ["notes.toml", "archive/notes.toml"]`
// has two single-file mounts, but `IsSingleFileDB` returns false
// (len(Paths) != 1) and would route every record through the
// glob-TOML walker — which mis-anchors against on-disk brackets
// shaped `[<file-relpath>.<bracket-key>]` and enumerates zero
// records. Reading `inst.SingleFileMount` (populated per-mount by
// db.Resolver.Instances) keeps the walker aligned with the per-file
// shape the writer uses (ops.Create routes through
// resolved.SingleFileMount likewise).
func searchFile(dbDecl schema.DB, inst db.Instance, plan searchPlan, q Query) ([]Result, error) {
	buf, err := os.ReadFile(inst.FilePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", inst.FilePath, err)
	}
	singleFile := inst.SingleFileMount
	backend, err := buildBackendForSearch(dbDecl, inst.Slug, singleFile)
	if err != nil {
		return nil, err
	}

	// List every declared section in the file. Per F10 the search no
	// longer narrows by type at the backend level — the user-facing id
	// has no type segment, so any per-type filtering routes through
	// the index from the caller (ops.Search).
	addresses, err := backend.List(buf, "")
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", inst.FilePath, err)
	}

	// Pre-cache the pelletier-decoded TOML root if we'll need it.
	var tomlRoot map[string]any
	if dbDecl.Format == schema.FormatTOML {
		if err := tomlv2.Unmarshal(buf, &tomlRoot); err != nil {
			return nil, fmt.Errorf("decode %s: %w", inst.FilePath, err)
		}
	}

	var hits []Result
	for _, addr := range addresses {
		// `addr` is the backend-relative bracket path:
		//   - TOML single-file: "<file-relpath>.<bracket-key>" (the bracket IS the id).
		//   - TOML multi-file:  "<type>.<id-tail>" (legacy bare form retained for the glob case).
		//   - MD any:           "<type>.<chain...>".
		fullAddr := fullAddress(dbDecl, inst, addr, singleFile)

		// Bracket-key prefix filter (F10): the user-facing id is
		// `<file-relpath>.<bracket-key>`; the prefix from parseScope
		// applies to the bracket-key portion of the id.
		bracketKey := bracketKeyForFilter(addr, dbDecl, inst, singleFile)
		if plan.idPrefix != "" && !strings.HasPrefix(bracketKey, plan.idPrefix) {
			continue
		}

		sec, ok, err := backend.Find(buf, addr)
		if err != nil {
			return nil, fmt.Errorf("find %s in %s: %w", addr, inst.FilePath, err)
		}
		if !ok {
			continue
		}
		recordBytes := buf[sec.Range[0]:sec.Range[1]]

		// Per F10: type lives in the index, not in the bracket. For
		// search field decoding we need to know the record's type to
		// look up its declared fields. Heuristic: pick the first
		// declared type on the db whose Field set covers the record's
		// observed fields. If multiple match, defer to the index.
		typeSt, ok := chooseTypeForRecord(dbDecl, addr)
		if !ok {
			continue
		}

		fields, err := decodeFields(dbDecl, typeSt, tomlRoot, buf, sec, addr)
		if err != nil {
			return nil, err
		}

		// Heterogeneous scope: a record whose type doesn't declare the
		// Match field or the named regex Field is a non-match (not an
		// error). MD body-only layout violation is ALWAYS loud.
		if err := mdLayoutCheck(dbDecl, typeSt, q); err != nil {
			return nil, err
		}
		skip := false
		if matchFilterErrors(typeSt, q.Match) != nil {
			skip = true
		}
		if !skip && fieldFilterError(typeSt, q.Field) != nil {
			skip = true
		}
		if skip {
			continue
		}

		if !matchFilter(typeSt, fields, q.Match) {
			continue
		}

		if q.Query != nil {
			matched, err := regexFilter(typeSt, fields, q.Query, q.Field)
			if err != nil {
				return nil, err
			}
			if !matched {
				continue
			}
		}

		hits = append(hits, Result{
			ID:     fullAddr,
			Bytes:  append([]byte(nil), recordBytes...),
			Fields: fields,
		})
	}
	return hits, nil
}

// chooseTypeForRecord picks a declared type on dbDecl to associate
// with a record at backend address `addr`. Per F10 the bracket IS
// the id and carries no type segment, so this is best-effort: pick
// the first declared type. Search routes type-narrowing via the
// index in ops.Search, so the type chosen here is only used for
// field-decoding driver state.
func chooseTypeForRecord(dbDecl schema.DB, addr string) (schema.SectionType, bool) {
	_ = addr
	for _, t := range dbDecl.Types {
		return t, true
	}
	return schema.SectionType{}, false
}

// bracketKeyForFilter extracts the bracket-key portion (the id-tail
// after the file-relpath) from a backend address.
func bracketKeyForFilter(addr string, dbDecl schema.DB, inst db.Instance, singleFile bool) string {
	if dbDecl.Format == schema.FormatTOML && singleFile {
		// addr is "<file-relpath>.<bracket-key>"; strip the prefix.
		if pre := inst.Slug + "."; strings.HasPrefix(addr, pre) {
			return strings.TrimPrefix(addr, pre)
		}
	}
	// Multi-file or MD: addr is the bracket-key / type-prefixed key.
	return addr
}

// buildBackendForSearch mirrors ops.buildBackend for the search path.
// Per F10:
//   - TOML single-file dbs: scanner anchors on the file-relpath
//     (every record's bracket starts with `<file-relpath>.`).
//   - TOML multi-file globs: dual-rule top-level + named-type backend
//     (F38d-2.16). The on-disk bracket form for these mounts is the
//     bracket-key alone (dot-free top-level bracket per F38d-2.15);
//     the named-type-prefix rule survives for legacy fixtures that
//     still encode the type in the bracket path. Mirrors production
//     ops.buildBackend's NewTopLevelBracketBackend() switch, with the
//     declared-type list folded in so legacy multi-instance fixtures
//     keep round-tripping. Without this fix, glob-TOML records (every
//     cascade record on disk) returned an empty List → list_sections /
//     search saw zero hits.
//   - MD section-mode: every declared type with its heading.
//   - MD file-as-record (F31): one type per db (mixed-mode prohibition);
//     dispatch to FileRecordBackend so heading=0 does not trip
//     md.NewBackend's [1, 6] validator (F38b).
func buildBackendForSearch(dbDecl schema.DB, fileRelPath string, singleFile bool) (record.Backend, error) {
	switch dbDecl.Format {
	case schema.FormatTOML:
		if singleFile {
			types := []record.DeclaredType{{Name: fileRelPath}}
			return toml.NewBackend(types), nil
		}
		types := make([]record.DeclaredType, 0, len(dbDecl.Types))
		for typeName := range dbDecl.Types {
			types = append(types, record.DeclaredType{Name: typeName})
		}
		return toml.NewBackendWithTopLevel(types), nil
	case schema.FormatMD:
		if schema.DBHasFileAsRecord(dbDecl) {
			fileType, st, ok := singleFileRecordType(dbDecl)
			if !ok {
				return nil, fmt.Errorf("search: db %q has file-as-record types but none resolved", dbDecl.Name)
			}
			types := []record.DeclaredType{{Name: fileType}}
			b, err := md.NewFileRecordBackend(types, fileType, st.BodyField)
			if err != nil {
				return nil, fmt.Errorf("search: build file-as-record backend for db %q: %w", dbDecl.Name, err)
			}
			return b, nil
		}
		types := make([]record.DeclaredType, 0, len(dbDecl.Types))
		for typeName, t := range dbDecl.Types {
			types = append(types, record.DeclaredType{
				Name:    typeName,
				Heading: t.Heading,
			})
		}
		b, err := md.NewBackend(types)
		if err != nil {
			return nil, fmt.Errorf("build MD backend for db %q: %w", dbDecl.Name, err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("%w: db %q format=%q",
			ErrUnsupportedFormat, dbDecl.Name, dbDecl.Format)
	}
}

// singleFileRecordType is the search-package mirror of
// ops.singleFileRecordType. Per F31's mixed-mode prohibition (load.go
// enforces) at most one file-as-record type can exist per db; this
// helper folds the lookup. Duplicated here rather than exported from
// schema to avoid premature package-API expansion (revisit when a
// fourth caller emerges; see F38b locked decisions).
func singleFileRecordType(dbDecl schema.DB) (string, schema.SectionType, bool) {
	for name, t := range dbDecl.Types {
		if t.IsFileRecord() {
			return name, t, true
		}
	}
	return "", schema.SectionType{}, false
}

// fullAddress returns the caller-visible id for a backend-relative
// record address. Per F10:
//   - TOML single-file: backend addr is "<file-relpath>.<bracket-key>"
//     which IS the id; pass through unchanged.
//   - TOML multi-file: backend addr is "<type>.<id-tail>" relative to
//     its file; prepend the file-relpath so the id is
//     "<file-relpath>.<type>.<id-tail>".
//   - MD section-mode: backend addr is "<type>.<chain...>"; prepend the
//     file-relpath.
//   - MD file-as-record (F31): the file IS the record; the id is the
//     file-relpath itself with no bracket-key or type suffix.
func fullAddress(dbDecl schema.DB, inst db.Instance, backendAddr string, singleFile bool) string {
	switch dbDecl.Format {
	case schema.FormatTOML:
		if singleFile {
			return backendAddr
		}
		return inst.Slug + "." + backendAddr
	case schema.FormatMD:
		if schema.DBHasFileAsRecord(dbDecl) {
			return inst.Slug
		}
		return inst.Slug + "." + backendAddr
	default:
		return backendAddr
	}
}

// decodeFields returns the parsed field map for one located record.
// For TOML: walk the already-decoded root via the record's bracket path.
// For MD section-mode (§5.3.3): the "body" field is everything after
// the heading line; only "body" is backed.
// For MD file-as-record (F31): frontmatter holds typed fields, body
// holds the markdown under the type's BodyField; every declared field
// is potentially backed.
func decodeFields(dbDecl schema.DB, typeSt schema.SectionType, tomlRoot map[string]any, buf []byte, sec record.Section, backendAddr string) (map[string]any, error) {
	switch dbDecl.Format {
	case schema.FormatTOML:
		return walkTOMLPath(tomlRoot, backendAddr)
	case schema.FormatMD:
		if typeSt.IsFileRecord() {
			return decodeFileRecordFields(buf, typeSt)
		}
		raw := buf[sec.Range[0]:sec.Range[1]]
		body := stripHeadingLine(raw)
		out := map[string]any{}
		// Only body is backed by the MVP layout; other declared fields
		// (if any) are absent — they stay absent in the map.
		if _, ok := typeSt.Fields["body"]; ok {
			out["body"] = string(body)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: db %q format=%q",
			ErrUnsupportedFormat, dbDecl.Name, dbDecl.Format)
	}
}

// decodeFileRecordFields parses the frontmatter + body of a
// file-as-record buffer and returns every declared field as a map.
// Frontmatter fields decode via md.DecodeFrontmatter; the BodyField
// is populated from the body bytes. Mirrors
// ops.extractFileRecordFields but populates every declared field
// (search needs the full record for regex/match filtering, not a
// caller-named subset).
func decodeFileRecordFields(buf []byte, st schema.SectionType) (map[string]any, error) {
	out := make(map[string]any, len(st.Fields))
	if len(buf) == 0 {
		return out, nil
	}
	front, body, err := md.SplitFrontmatter(buf)
	if err != nil {
		return nil, fmt.Errorf("file-as-record: split frontmatter: %w", err)
	}
	var fm map[string]any
	if front != nil {
		fm, err = md.DecodeFrontmatter(front)
		if err != nil {
			return nil, fmt.Errorf("file-as-record: decode frontmatter: %w", err)
		}
	}
	for name := range st.Fields {
		if name == st.BodyField {
			out[name] = string(body)
			continue
		}
		if v, ok := fm[name]; ok {
			out[name] = v
		}
	}
	return out, nil
}

// walkTOMLPath descends the pelletier-decoded root by the dotted segs of
// backendAddr and returns the leaf table's fields. A missing segment
// returns an empty map (the record was listed but somehow has no
// decoded state — treat as empty rather than erroring; callers can still
// filter).
func walkTOMLPath(root map[string]any, backendAddr string) (map[string]any, error) {
	cursor := root
	for seg := range strings.SplitSeq(backendAddr, ".") {
		next, ok := cursor[seg]
		if !ok {
			return map[string]any{}, nil
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return map[string]any{}, nil
		}
		cursor = nextMap
	}
	// Shallow clone so callers cannot mutate our decoded tree.
	out := make(map[string]any, len(cursor))
	maps.Copy(out, cursor)
	return out, nil
}

// stripHeadingLine returns raw with the first line (heading) and at
// most one directly-following blank line removed. Mirrors the MVP body-
// only layout in internal/ops/fields.go.
func stripHeadingLine(raw []byte) []byte {
	_, rest, ok := bytes.Cut(raw, []byte{'\n'})
	if !ok {
		return nil
	}
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	return rest
}

// validateScopeNames runs at Run entry for type-unconstrained scope
// and errors when a Match/Field name is declared on zero types in
// scope. A name declared on at least one type in scope passes — the
// existing per-record silent-skip in searchFile correctly handles the
// heterogeneous case where some types declare the field and others
// don't. This closes the "pure typo under bare <db> scope returns
// silent zero-hits" hole (V2-PLAN §12.7 Falsification finding #2).
func validateScopeNames(reg schema.Registry, plan searchPlan, q Query) error {
	names := make([]string, 0, len(q.Match)+1)
	for name := range q.Match {
		names = append(names, name)
	}
	if q.Field != "" {
		names = append(names, q.Field)
	}
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		found := false
		for _, dbName := range plan.dbOrder {
			dbDecl := reg.DBs[dbName]
			for _, t := range dbDecl.Types {
				if _, ok := t.Fields[name]; ok {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: %q not declared on any type in scope",
				ErrUnknownField, name)
		}
	}
	return nil
}

// validateScopeMatchSemantics rejects Match pairs whose field is
// declared as array/table on EVERY type in scope — a non-scalar match
// is a typed-contract violation and must surface ErrUnscalarMatch
// loudly. If at least one type in scope declares the field as scalar,
// the heterogeneous-scope rule applies (per-record silent skip).
func validateScopeMatchSemantics(reg schema.Registry, plan searchPlan, q Query) error {
	for name := range q.Match {
		anyScalar := false
		anyDeclared := false
		var lastNonScalar schema.Type
		for _, dbName := range plan.dbOrder {
			dbDecl := reg.DBs[dbName]
			for _, t := range dbDecl.Types {
				f, ok := t.Fields[name]
				if !ok {
					continue
				}
				anyDeclared = true
				switch f.Type {
				case schema.TypeArray, schema.TypeTable:
					lastNonScalar = f.Type
				default:
					anyScalar = true
				}
			}
		}
		if anyDeclared && !anyScalar {
			return fmt.Errorf("%w: field %q is %s", ErrUnscalarMatch, name, lastNonScalar)
		}
	}
	return nil
}

// mdLayoutCheck rejects Match keys and the named regex Field when the
// db is MD-format and the name is a declared non-body field. Under the
// body-only layout (§5.3.3) only "body" is readable, so a declared
// non-body field is a typed-contract lie: the schema claims it exists
// but the layout has no on-disk representation. Fails loudly to match
// the contract ops/fields.go:extractMDFields enforces on the `get`
// path — both entry points route through md.CheckBackableFields so
// they cannot drift (V2-PLAN §12.7 Falsification finding #30).
//
// Names not declared on typeSt are left to matchFilterErrors /
// fieldFilterError (the unknown-field surface, scope-dependent).
//
// File-as-record types (F31) bypass this check: their layout backs
// every declared field (frontmatter + body), so md.CheckBackableFields'
// body-only restriction does not apply.
func mdLayoutCheck(dbDecl schema.DB, typeSt schema.SectionType, q Query) error {
	if dbDecl.Format != schema.FormatMD {
		return nil
	}
	if typeSt.IsFileRecord() {
		return nil
	}
	names := make([]string, 0, len(q.Match)+1)
	for name := range q.Match {
		if _, declared := typeSt.Fields[name]; declared {
			names = append(names, name)
		}
	}
	if q.Field != "" {
		if _, declared := typeSt.Fields[q.Field]; declared {
			names = append(names, q.Field)
		}
	}
	if err := md.CheckBackableFields(names); err != nil {
		return fmt.Errorf("%w: %s", ErrUnknownField, err.Error())
	}
	return nil
}

// fieldFilterError validates Query.Field against a declared type. Empty
// field is always legal (means "scan every string field"). A non-empty
// field must be declared and typed string.
func fieldFilterError(typeSt schema.SectionType, field string) error {
	if field == "" {
		return nil
	}
	f, ok := typeSt.Fields[field]
	if !ok {
		return fmt.Errorf("%w: regex field %q not declared on %q",
			ErrUnknownField, field, typeSt.Name)
	}
	if f.Type != schema.TypeString {
		return fmt.Errorf("%w: regex field %q is %s (must be string)",
			ErrUnknownField, field, f.Type)
	}
	return nil
}

// matchFilterErrors returns the first structural error (unknown field,
// non-scalar match) so the caller can fail loudly. It never silently
// drops a match pair.
func matchFilterErrors(typeSt schema.SectionType, match map[string]any) error {
	for name := range match {
		f, ok := typeSt.Fields[name]
		if !ok {
			return fmt.Errorf("%w: %q not declared on %q", ErrUnknownField, name, typeSt.Name)
		}
		switch f.Type {
		case schema.TypeArray, schema.TypeTable:
			return fmt.Errorf("%w: field %q is %s", ErrUnscalarMatch, name, f.Type)
		}
	}
	return nil
}

// matchFilter evaluates the Match pairs against the decoded record. It
// assumes matchFilterErrors has already run to vet each pair.
func matchFilter(typeSt schema.SectionType, fields map[string]any, match map[string]any) bool {
	for name, want := range match {
		f := typeSt.Fields[name]
		got, present := fields[name]
		if !present {
			return false
		}
		if !scalarEqual(f.Type, got, want) {
			return false
		}
	}
	return true
}

// scalarEqual compares a decoded field value against the want value per
// schema type. The want value is whatever the caller passed in — for
// MCP JSON that's always numeric as float64 or json.Number.
func scalarEqual(t schema.Type, got, want any) bool {
	switch t {
	case schema.TypeInteger, schema.TypeFloat:
		return numericEqual(got, want)
	case schema.TypeBoolean:
		gb, gok := toBool(got)
		wb, wok := toBool(want)
		if !gok || !wok {
			return false
		}
		return gb == wb
	default:
		// string, enum, datetime — compare via string fmt.
		return fmt.Sprintf("%v", got) == fmt.Sprintf("%v", want)
	}
}

func numericEqual(a, b any) bool {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if !aok || !bok {
		return false
	}
	return af == bf
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case string:
		return 0, false
	default:
		return 0, false
	}
}

func toBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

// regexFilter evaluates q against the record's string fields. When field
// is set, only that named field is scanned; otherwise every declared
// string-typed field is scanned.
func regexFilter(typeSt schema.SectionType, fields map[string]any, re *regexp.Regexp, field string) (bool, error) {
	if field != "" {
		f, ok := typeSt.Fields[field]
		if !ok {
			return false, fmt.Errorf("%w: regex field %q not declared on %q",
				ErrUnknownField, field, typeSt.Name)
		}
		if f.Type != schema.TypeString {
			return false, fmt.Errorf("%w: regex field %q is %s (must be string)",
				ErrUnknownField, field, f.Type)
		}
		return regexOnField(re, fields, field), nil
	}
	for name, f := range typeSt.Fields {
		if f.Type != schema.TypeString {
			continue
		}
		if regexOnField(re, fields, name) {
			return true, nil
		}
	}
	return false, nil
}

func regexOnField(re *regexp.Regexp, fields map[string]any, name string) bool {
	raw, ok := fields[name]
	if !ok {
		return false
	}
	s, ok := raw.(string)
	if !ok {
		s = fmt.Sprintf("%v", raw)
	}
	return re.MatchString(s)
}
