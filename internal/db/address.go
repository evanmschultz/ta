package db

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanmschultz/ta/internal/schema"
)

// Address is the structured view of a dotted section path under the
// PLAN §12.17.9 Phase 9.2 grammar:
//
//	<file-relpath>.<type>.<id-tail>
//
// FileRelPath is the path-segments-after-the-mount-static-prefix joined
// with `.` (extension stripped). Example: mount `["workflow/*/db"]`,
// file `workflow/ta/db.toml` → static prefix `workflow/`, residual
// `ta/db.toml` → ext-stripped `ta/db` → dotted `ta.db`.
//
// Type stays in the address until Phase 9.4 moves it to a `--type`
// flag. ID is the bracket-path tail joined with `.` so deep TOML
// brackets (`[build_task.task_001]`) and single-segment MD slugs
// (`installation`) round-trip through the same field.
//
// DBName is the resolved db (the registry entry whose mount matched
// the file-relpath segments). FilePath is the absolute on-disk path.
type Address struct {
	DBName      string
	FileRelPath string
	Type        string
	ID          string
	FilePath    string

	// Mount is the mount-entry string from db.Paths that matched
	// (e.g. "workflow/*/db"). Bracket-form selection (db-prefixed
	// vs bare) and instance-relative path derivations look at this
	// rather than re-deriving from db state.
	Mount string

	// SingleFileMount is true when Mount resolves to exactly one
	// concrete file (no glob, no trailing slash). Drives the
	// bracket-form choice for TOML payloads. Mirrors
	// schema.SingleFileMount(Mount).
	SingleFileMount bool
}

// Canonical returns the round-trippable dotted-string form of addr.
func (a Address) Canonical() string {
	parts := make([]string, 0, 3)
	if a.FileRelPath != "" {
		parts = append(parts, a.FileRelPath)
	}
	parts = append(parts, a.Type)
	if a.ID != "" {
		parts = append(parts, a.ID)
	}
	return strings.Join(parts, ".")
}

// ParseAddress splits section under the new file-relpath grammar.
// Iterates the registry's dbs in stable name order; for each db
// iterates Paths entries; for each entry computes the static prefix
// (everything up to the first `*`, or up to the last segment for non-
// glob mounts) and the suffix shape (segments after the static prefix,
// `*` matching one segment). The file-relpath portion of section is
// matched against the mount's suffix shape; on success, the next
// segment is the `<type>` and the rest is the `<id-tail>`. First
// matching db wins.
//
// Returns ErrUnknownDB when no db's mount accepts the section (typo
// at the file-relpath level), ErrUnknownType when the type segment is
// not declared on the resolved db, and ErrBadAddress on grammar
// violations (empty, leading/trailing/empty segments, too few
// segments after the file-relpath portion).
//
// FilePath is reconstructed by re-joining the mount's static prefix
// with the file-relpath-derived directory plus the appropriate file
// extension (db.Format).
func (r *Resolver) ParseAddress(section string) (Address, schema.DB, error) {
	if section == "" {
		return Address{}, schema.DB{}, fmt.Errorf("%w: empty", ErrBadAddress)
	}
	parts := strings.Split(section, ".")
	for _, p := range parts {
		if p == "" {
			return Address{}, schema.DB{}, fmt.Errorf(
				"%w: %q has empty segment", ErrBadAddress, section)
		}
	}

	// Iterate dbs in stable order so first-match is deterministic.
	dbNames := make([]string, 0, len(r.registry.DBs))
	for name := range r.registry.DBs {
		dbNames = append(dbNames, name)
	}
	sort.Strings(dbNames)

	// Two-phase match: non-collection (specific) mounts win over
	// collection (catch-all) mounts. Within each phase, dbs are tried
	// in stable name order. Phase 9.2 picks this tiebreaker because a
	// collection root mounts every descendant file — without the
	// preference, a section that could match both a single-file mount
	// and a sibling collection would non-deterministically bind to the
	// alphabetical-first db.
	for _, collectionPhase := range []bool{false, true} {
		for _, dbName := range dbNames {
			dbDecl := r.registry.DBs[dbName]
			for _, mount := range dbDecl.Paths {
				isColl := strings.HasSuffix(mount, "/") || mount == "."
				if isColl != collectionPhase {
					continue
				}
				addr, ok, err := tryParseAgainstMount(parts, dbDecl, mount, r.root)
				if err != nil {
					return Address{}, schema.DB{}, err
				}
				if !ok {
					continue
				}
				if _, declared := dbDecl.Types[addr.Type]; !declared {
					return Address{}, schema.DB{}, fmt.Errorf(
						"%w: %q on db %q",
						ErrUnknownType, addr.Type, dbDecl.Name)
				}
				return addr, dbDecl, nil
			}
		}
	}

	return Address{}, schema.DB{}, fmt.Errorf(
		"%w: no db mount matches %q", ErrUnknownDB, section)
}

// tryParseAgainstMount attempts to parse parts against one mount entry
// of one db. Returns (addr, true, nil) on a successful match,
// (zero, false, nil) when the mount's expected file-relpath shape
// does not match parts, and (zero, false, err) for hard grammar
// errors that should short-circuit the search (e.g. too few segments
// AFTER a successful file-relpath match means ErrBadAddress).
func tryParseAgainstMount(parts []string, dbDecl schema.DB, mount, root string) (Address, bool, error) {
	// Expand `~/` once at the top so the staticPrefix split, the
	// collection check, and the on-disk path build all see a literal
	// home-anchored path rather than carrying a `~` segment forward.
	// PLAN §12.17.9 Phase 9.2 treats `~/...` mounts as home-relative
	// across both the address parser and the resolver; without this,
	// `ResolveRead` / `ResolveWrite` would build `<root>/~/...` (a
	// literal `~` directory under project root), corrupting the tree
	// on Create. Mirrors the resolver.expandMount call site.
	base, mountAfterHome, err := resolveHome(root, mount)
	if err != nil {
		return Address{}, false, fmt.Errorf(
			"db %q: mount %q: %w", dbDecl.Name, mount, err)
	}
	staticPrefix, residualSegs := splitMountSegments(mountAfterHome)
	// `residualSegs` is the list of segments after the static prefix.
	// Each segment is either `*` (matches any one path segment) or a
	// literal. The mount's last residual segment is the file basename
	// (sans extension) — for `workflow/*/db` it's `db`; for `plans` it
	// is `plans`; for `docs/` it is "" (collection — every file under
	// docs/ matches).
	//
	// The address must START with N segments matching residualSegs (one
	// dotted segment per path segment), then carry at least <type> and
	// <id> after.
	collection := strings.HasSuffix(mountAfterHome, "/") || mountAfterHome == "."

	if collection {
		// Collection: any descendant file under staticPrefix matches.
		// The address must have at least <type>.<id> + 1 file-relpath
		// segment (the leaf file basename pre-ext). The locked design
		// (PLAN §12.17.9 Phase 9.2) says LEFTMOST declared-type-name
		// wins: scan parts from the left starting at index 1 (so the
		// file-relpath has at least one segment); the first segment
		// whose value matches a declared type name on dbDecl is the
		// type segment. Everything before is file-relpath; everything
		// after is id-tail.
		idx := firstDeclaredTypeIndex(parts, dbDecl)
		if idx < 1 {
			// type must be at least at index 1 so file-relpath has at
			// least one segment.
			return Address{}, false, nil
		}
		fileRelSegs := parts[:idx]
		typeName := parts[idx]
		var idTail string
		if idx+1 < len(parts) {
			idTail = strings.Join(parts[idx+1:], ".")
		}
		// Build absolute file path from staticPrefix + fileRelSegs +
		// last-segment-as-filename + extension.
		filePath, ok := buildFilePathCollection(base, staticPrefix, fileRelSegs, dbDecl.Format)
		if !ok {
			return Address{}, false, nil
		}
		return Address{
			DBName:          dbDecl.Name,
			FileRelPath:     strings.Join(fileRelSegs, "."),
			Type:            typeName,
			ID:              idTail,
			FilePath:        filePath,
			Mount:           mount,
			SingleFileMount: false,
		}, true, nil
	}

	// Non-collection: residualSegs is fixed-length. The first
	// len(residualSegs) parts must satisfy each segment.
	//
	// Literal segments match by string equality, with one tolerance:
	// the mount's leaf segment may carry an explicit ".<format>"
	// extension (e.g. `["plans.toml"]`). The address never carries
	// the extension, so we ext-strip the leaf segment before
	// comparing. Glob `*` matches anything non-empty.
	expected := stripFormatExt(residualSegs, dbDecl.Format)
	if len(parts) < len(expected)+1 {
		// Need at least <type> after file-relpath.
		return Address{}, false, nil
	}
	for i, seg := range expected {
		if seg == "*" {
			continue
		}
		if parts[i] != seg {
			return Address{}, false, nil
		}
	}
	fileRelSegs := parts[:len(expected)]
	if len(parts) < len(expected)+2 {
		// We have <file-relpath>.<type> but no <id> — grammar error.
		return Address{}, true, fmt.Errorf(
			"%w: missing id-tail after type segment in %q",
			ErrBadAddress, strings.Join(parts, "."))
	}
	typeName := parts[len(expected)]
	idTail := strings.Join(parts[len(expected)+1:], ".")

	// Build absolute file path: staticPrefix + (fileRelSegs joined
	// with "/") + extension.
	filePath := buildFilePathFixed(base, staticPrefix, fileRelSegs, dbDecl.Format)

	return Address{
		DBName:          dbDecl.Name,
		FileRelPath:     strings.Join(fileRelSegs, "."),
		Type:            typeName,
		ID:              idTail,
		FilePath:        filePath,
		Mount:           mount,
		SingleFileMount: schema.SingleFileMount(mount),
	}, true, nil
}

// stripFormatExt returns residualSegs with the format extension
// stripped from the leaf segment if present. The mount's leaf segment
// may be declared with or without the explicit extension; both forms
// must produce the same file-relpath because the address never carries
// the extension.
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
// AFTER staticPrefix (excluding any trailing-slash collection marker).
//
// Examples:
//   - "plans"           → "", ["plans"]
//   - "workflow/*/db"   → "workflow/", ["*", "db"]
//   - "docs/"           → "docs/", []
//   - "docs/api"        → "docs/", ["api"]
//   - "."               → "", []                 (treated as collection root at project root)
//   - "README"          → "", ["README"]
func splitMountSegments(mount string) (string, []string) {
	if mount == "." {
		// Project-root collection: any file under root matches.
		return "", []string{}
	}
	if strings.HasSuffix(mount, "/") {
		// Collection: residualSegs is empty; staticPrefix is the dir.
		return mount, []string{}
	}
	// Find first `*` position by segment.
	segs := strings.Split(mount, "/")
	starIdx := -1
	for i, s := range segs {
		if s == "*" || strings.Contains(s, "*") {
			starIdx = i
			break
		}
	}
	if starIdx >= 0 {
		// staticPrefix = everything up to (and including) the slash
		// before the first `*`.
		prefix := strings.Join(segs[:starIdx], "/")
		if prefix != "" {
			prefix += "/"
		}
		return prefix, segs[starIdx:]
	}
	// No `*`: static prefix is everything before the last segment;
	// residual is the last segment alone (the file basename).
	if len(segs) == 1 {
		return "", segs
	}
	prefix := strings.Join(segs[:len(segs)-1], "/") + "/"
	return prefix, []string{segs[len(segs)-1]}
}

// firstDeclaredTypeIndex returns the smallest i in [1, len(parts)) such
// that parts[i] is a declared type name on dbDecl, or -1 if none. The
// locked Phase 9.2 design (PLAN §12.17.9) is LEFTMOST-wins so that an
// address like `install.prereqs.section.title` parses as
// file-relpath=`install.prereqs`, type=`section`, id=`title` rather
// than greedily consuming as many segments as possible into the
// file-relpath. Used by the collection mount-matcher to decide where
// the file-relpath ends and the <type> begins.
func firstDeclaredTypeIndex(parts []string, dbDecl schema.DB) int {
	for i := 1; i < len(parts); i++ {
		if _, ok := dbDecl.Types[parts[i]]; ok {
			return i
		}
	}
	return -1
}

// buildFilePathFixed constructs the absolute file path for a non-glob,
// non-collection mount. fileRelSegs is the parsed file-relpath; for a
// pure-literal mount these mirror the mount's residual segments. The
// extension is derived from dbDecl.Format.
func buildFilePathFixed(root, staticPrefix string, fileRelSegs []string, format schema.Format) string {
	rel := staticPrefix + strings.Join(fileRelSegs, "/") + "." + string(format)
	return filepath.Join(root, filepath.FromSlash(rel))
}

// buildFilePathCollection constructs the absolute file path for a
// collection-rooted mount. staticPrefix is the collection root
// (trailing slash); fileRelSegs is the dotted file-relpath split back
// to path segments. The leaf segment is the basename; the extension
// comes from dbDecl.Format. Returns ok=false when fileRelSegs is empty.
func buildFilePathCollection(root, staticPrefix string, fileRelSegs []string, format schema.Format) (string, bool) {
	if len(fileRelSegs) == 0 {
		return "", false
	}
	rel := staticPrefix + strings.Join(fileRelSegs, "/") + "." + string(format)
	return filepath.Join(root, filepath.FromSlash(rel)), true
}
