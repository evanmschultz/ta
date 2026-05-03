package db

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/evanmschultz/ta/internal/schema"
)

// Resolved is the structured view of an id. Per F10 (PLAN §12.17.9):
//
//	id := <FileRelPath>.<BracketKey>
//
// FileRelPath is the path-segments-after-the-mount-static-prefix joined
// with `.` (extension stripped). Example: mount `["workflow/*/db.toml"]`,
// file `workflow/ta/db.toml` → static prefix `workflow/`, residual
// `ta/db.toml` → ext-stripped `ta/db` → dotted `ta.db`.
//
// BracketKey is the bracket-tail-after-file-relpath. Type does NOT live
// in the id — it lives in the index. The whole id is the bracket header
// on disk: `[plans.demo-1]` is one record, the id is `plans.demo-1`,
// the FileRelPath is `plans` (file `plans.toml`), the BracketKey is
// `demo-1`.
//
// DBName is the resolved db (the registry entry whose mount matched
// the file-relpath segments). FilePath is the absolute on-disk path.
type Resolved struct {
	DBName      string
	FileRelPath string
	BracketKey  string
	FilePath    string

	// Mount is the mount-entry string from db.Paths that matched
	// (e.g. "workflow/*/db.toml" or "plans.toml"). Bracket-form choice
	// is no longer driven by the mount shape post-F10 — bracket = id —
	// but the field is retained for callers that need the originating
	// mount string (e.g. for error messages).
	Mount string

	// SingleFileMount records whether the mount resolves to exactly
	// one concrete file. Post-F10 it does NOT drive bracket-form
	// selection (bracket = id, period). It is retained so callers that
	// need the mount-shape view (e.g. resolver.Instances enumeration)
	// can see it without re-deriving from schema state.
	SingleFileMount bool
}

// Canonical returns the round-trippable id form of r. For scope-prefix
// resolutions where BracketKey is empty, returns just FileRelPath.
func (r Resolved) Canonical() string {
	if r.BracketKey == "" {
		return r.FileRelPath
	}
	if r.FileRelPath == "" {
		return r.BracketKey
	}
	return r.FileRelPath + "." + r.BracketKey
}

// ResolveID parses id under the F10 grammar and returns the Resolved
// view + the matching db declaration. Iterates the registry's dbs in
// stable name order; for each db iterates Paths entries; for each
// entry computes the static prefix and matches the id's leading
// segments against the mount's expected file-relpath shape. The
// remaining segments form the BracketKey. First matching db wins.
//
// Returns ErrBadID on grammar violations (empty, leading/trailing/
// empty segments, missing bracket-key after file-relpath), and
// ErrIDDoesNotMatchAnyDB when no mount accepts the id.
//
// FilePath is reconstructed by re-joining the mount's static prefix
// with the file-relpath-derived directory plus the format extension
// inferred from the mount.
func (r *Resolver) ResolveID(id string) (Resolved, schema.DB, error) {
	if id == "" {
		return Resolved{}, schema.DB{}, fmt.Errorf("%w: empty", ErrBadID)
	}
	parts := strings.Split(id, ".")
	if slices.Contains(parts, "") {
		return Resolved{}, schema.DB{}, fmt.Errorf(
			"%w: %q has empty segment", ErrBadID, id)
	}

	// Iterate dbs in stable order so first-match is deterministic.
	dbNames := make([]string, 0, len(r.registry.DBs))
	for name := range r.registry.DBs {
		dbNames = append(dbNames, name)
	}
	sort.Strings(dbNames)

	for _, dbName := range dbNames {
		dbDecl := r.registry.DBs[dbName]
		for _, mount := range dbDecl.Paths {
			res, ok, err := tryParseAgainstMount(parts, dbDecl, mount, r.root)
			if err != nil {
				return Resolved{}, schema.DB{}, err
			}
			if !ok {
				continue
			}
			return res, dbDecl, nil
		}
	}

	return Resolved{}, schema.DB{}, fmt.Errorf(
		"%w: %q", ErrIDDoesNotMatchAnyDB, id)
}

// ResolveIDInDB is the type-aware companion to ResolveID. It is the
// resolver used by Create / Update / Delete when the caller has
// supplied a db-qualified --type (e.g. `agents.agent`): the caller's
// declared db is the authoritative anchor, so the mount-iteration
// loop is constrained to that db only. A bare 2-segment id like
// `<group>.<name>` no longer falls through alphabetically to a
// different db (e.g. `claude_agents/*.md`) when the caller's intent
// was `agents/*/*.md` — it errors with the expected shape derived
// from the named db's mount wildcards.
//
// On miss, returns an error of the form:
//
//	db %q does not accept id %q (expected shape: <description>; got %d segments, need %d)
//
// where the description is derived from the db's mount(s) — for a
// `agents/*/*.md` mount the shape is `<group>.<name>.<bracket-key>`.
//
// Returns ErrIDDoesNotMatchAnyDB when dbName is not in the registry
// (mirrors the resolver's overall miss sentinel for a consistent
// error shape across the surface). Read paths that do not pass
// --type (Get, List, search) keep using plain ResolveID — only the
// mutation-with-type path benefits from the constraint.
func (r *Resolver) ResolveIDInDB(id, dbName string) (Resolved, schema.DB, error) {
	if id == "" {
		return Resolved{}, schema.DB{}, fmt.Errorf("%w: empty", ErrBadID)
	}
	dbDecl, ok := r.registry.DBs[dbName]
	if !ok {
		return Resolved{}, schema.DB{}, fmt.Errorf(
			"%w: db %q not declared in registry", ErrIDDoesNotMatchAnyDB, dbName)
	}
	parts := strings.Split(id, ".")
	if slices.Contains(parts, "") {
		return Resolved{}, schema.DB{}, fmt.Errorf(
			"%w: %q has empty segment", ErrBadID, id)
	}
	for _, mount := range dbDecl.Paths {
		res, ok, err := tryParseAgainstMount(parts, dbDecl, mount, r.root)
		if err != nil {
			return Resolved{}, schema.DB{}, err
		}
		if !ok {
			continue
		}
		return res, dbDecl, nil
	}
	return Resolved{}, schema.DB{}, fmt.Errorf(
		"%w: db %q does not accept id %q (%s; got %d segments)",
		ErrIDDoesNotMatchAnyDB, dbName, id, expectedShapeForDB(dbDecl), len(parts))
}

// expectedShapeForDB renders a human-readable expected-shape
// description for dbDecl, derived from the db's mount wildcards. A
// single-mount db produces a single shape; multi-mount dbs produce a
// "one of: ..." list. Used by ResolveIDInDB's miss-error to tell the
// caller exactly what id grammar the named db accepts.
func expectedShapeForDB(dbDecl schema.DB) string {
	if len(dbDecl.Paths) == 0 {
		return "expected shape: <unknown> (db has no mounts)"
	}
	shapes := make([]string, 0, len(dbDecl.Paths))
	for _, mount := range dbDecl.Paths {
		shape, segs := mountExpectedShape(mount, dbDecl.Format)
		shapes = append(shapes, fmt.Sprintf("%s, need %d", shape, segs))
	}
	if len(shapes) == 1 {
		return "expected shape: " + shapes[0]
	}
	return "expected one of: " + strings.Join(shapes, " | ")
}

// mountExpectedShape derives a human-readable id-grammar template
// from one mount entry (e.g. `agents/*/*.md` →
// `<group>.<name>.<bracket-key>`, segs=3). The caller composes the
// template with a "got N segments, need M" suffix.
//
// Wildcard-segment names are synthesized in declaration order:
// `<seg-1>`, `<seg-2>`, etc. (kept generic — without inspecting the
// concrete filesystem we cannot infer semantic names like `group`).
// Each wildcard contributes one segment; the leaf static segment
// (with format extension stripped) contributes one segment when
// non-empty; the bracket-key contributes one segment.
func mountExpectedShape(mount string, format schema.Format) (string, int) {
	_, residualSegs := splitMountSegments(mount)
	expected := stripFormatExt(residualSegs, format)
	parts := make([]string, 0, len(expected)+1)
	wildIdx := 0
	for _, seg := range expected {
		if seg == "*" {
			wildIdx++
			parts = append(parts, fmt.Sprintf("<seg-%d>", wildIdx))
			continue
		}
		// Literal residual segment (e.g. `db` in `workflow/*/db.toml`).
		parts = append(parts, seg)
	}
	parts = append(parts, "<bracket-key>")
	return "expected shape: " + strings.Join(parts, "."), len(parts)
}

// tryParseAgainstMount attempts to parse parts against one mount entry
// of one db under the F10 id grammar (and the F31 file-as-record
// relaxation). Returns (resolved, true, nil) on a successful match,
// (zero, false, nil) when the mount's expected file-relpath shape
// does not match parts, and (zero, false, err) for hard grammar
// errors.
//
// Collection mounts (`docs/`, `.`) are rejected at schema-load time
// (ErrCollectionMountUnsupported); this helper assumes the mount has
// a recognized extension.
//
// Per F31: when dbDecl carries any file-as-record type, an id whose
// segment count exactly matches the expected file-relpath length
// (no bracket-key tail) is accepted with BracketKey="". The whole
// file IS the record on file-as-record dbs; there is no per-section
// anchor. Section-only dbs keep the strict "one bracket-key segment
// required" rule.
func tryParseAgainstMount(parts []string, dbDecl schema.DB, mount, root string) (Resolved, bool, error) {
	base, mountAfterHome, err := resolveHome(root, mount)
	if err != nil {
		return Resolved{}, false, fmt.Errorf(
			"db %q: mount %q: %w", dbDecl.Name, mount, err)
	}
	staticPrefix, residualSegs := splitMountSegments(mountAfterHome)

	// Schema-load enforces every mount has a recognized extension;
	// strip it from the leaf residual segment so the id (which never
	// carries the extension) compares cleanly.
	expected := stripFormatExt(residualSegs, dbDecl.Format)
	hasFileRecord := schema.DBHasFileAsRecord(dbDecl)
	minLen := len(expected) + 1
	if hasFileRecord {
		// File-as-record dbs accept ids with EXACTLY the file-relpath
		// length (no bracket-key) — the whole file is the one record.
		minLen = len(expected)
	}
	if len(parts) < minLen {
		return Resolved{}, false, nil
	}
	for i, seg := range expected {
		if seg == "*" {
			continue
		}
		if parts[i] != seg {
			return Resolved{}, false, nil
		}
	}
	fileRelSegs := parts[:len(expected)]
	bracketKey := strings.Join(parts[len(expected):], ".")

	// Build absolute file path: staticPrefix + (fileRelSegs joined
	// with "/") + extension.
	filePath := buildFilePathFixed(base, staticPrefix, fileRelSegs, dbDecl.Format)

	return Resolved{
		DBName:          dbDecl.Name,
		FileRelPath:     strings.Join(fileRelSegs, "."),
		BracketKey:      bracketKey,
		FilePath:        filePath,
		Mount:           mount,
		SingleFileMount: schema.SingleFileMount(mount),
	}, true, nil
}

// stripFormatExt returns residualSegs with the format extension
// stripped from the leaf segment if present.
func stripFormatExt(residualSegs []string, format schema.Format) []string {
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

// splitMountSegments returns (staticPrefix, residualSegs) for mount.
// staticPrefix is everything up to (and including) the slash before the
// first `*`. If mount has no `*`, staticPrefix is everything before the
// last slash (or "" if no slash). residualSegs is the path-segments
// AFTER staticPrefix.
//
// Examples:
//   - "plans.toml"            → "", ["plans.toml"]
//   - "workflow/*/db.toml"    → "workflow/", ["*", "db.toml"]
//   - "docs/api.md"           → "docs/", ["api.md"]
//   - "README.md"             → "", ["README.md"]
//
// Trailing-slash collection mounts and the "." project-root mount are
// rejected at schema-load time post-F10 (ErrCollectionMountUnsupported).
// They cannot reach this function because every well-formed registry
// has been pre-validated; we still defend against them by returning a
// single empty residual which makes any id parse fail downstream.
func splitMountSegments(mount string) (string, []string) {
	if mount == "." || strings.HasSuffix(mount, "/") {
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

// buildFilePathFixed constructs the absolute file path for a non-glob
// mount. fileRelSegs is the parsed file-relpath; the extension is
// derived from dbDecl.Format.
func buildFilePathFixed(root, staticPrefix string, fileRelSegs []string, format schema.Format) string {
	rel := staticPrefix + strings.Join(fileRelSegs, "/") + "." + string(format)
	return filepath.Join(root, filepath.FromSlash(rel))
}
