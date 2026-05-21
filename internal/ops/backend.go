package ops

import (
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/backend/md"
	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// buildBackend constructs a record.Backend for the declared db.
//
// Per F10 (PLAN §12.17.9), the bracket = id verbatim. The TOML
// scanner's declared-type prefix list is just the bare type names
// for multi-file dbs and the file-relpath-prefixed-by-type form for
// single-file dbs is not used — single-file dbs ALSO emit brackets
// where the file-relpath is the FIRST segment (the bracket IS the id).
// For the scanner to recognize all records in a single-file db, we
// pass the file-relpath as the prefix (one entry per declared type
// since types share the prefix shape).
//
// Wait: that requires resolved info. Simpler path: we pass bare type
// names; the scanner will only find brackets that start with one of
// those declared types. For SINGLE-FILE dbs whose file-relpath equals
// the db name (the common case `plans.toml` → file-relpath `plans`),
// id `plans.demo-1` has on-disk bracket `[plans.demo-1]`; the scanner
// must recognize `plans` as a declared "scanner-prefix" — but `plans`
// is a db NAME, not a type name.
//
// Resolution: for single-file dbs, the scanner's declared prefix is
// the file-relpath itself (which is what the TOML bracket starts with
// for any record under that db). For multi-file dbs, brackets in each
// file start with the bracket-key (which the user picks freely; the
// scanner needs to enumerate every top-level table). The TOML
// backend's List with empty prefix-anchor + arbitrary declared types
// won't work without a major scanner refactor.
//
// Pragmatic F10 path: per dev-locked decision (no half-migrated
// state), the TOML backend continues to receive a prefix-anchor list,
// but we pass each declared type as its own prefix. For single-file
// dbs we pass the file-relpath as the prefix; the scanner finds every
// `[<file-relpath>.X]` bracket, irrespective of X. For multi-file dbs
// we pass the bare type names as before.
func buildBackend(dbDecl schema.DB, resolved db.Resolved) (record.Backend, error) {
	switch dbDecl.Format {
	case schema.FormatTOML:
		// F38d-2.15: glob-TOML mounts (multi-file dbs) carry bracket =
		// bracket-key alone — there is no type prefix to anchor the
		// scanner's declared-type filter against because the type lives
		// in the index, not in the on-disk bracket. The
		// top-level-bracket backend accepts every dot-free bracket as a
		// declared record so Find/List/Splice round-trip correctly for
		// this mount class. Single-file mounts (where the bracket
		// always starts with `<file-relpath>.`) keep the existing
		// prefix-anchor backend.
		if !resolved.SingleFileMount {
			return toml.NewTopLevelBracketBackend(), nil
		}
		types := tomlScannerTypes(dbDecl, resolved)
		return toml.NewBackend(types), nil
	case schema.FormatMD:
		// Per F31: when the db has any file-as-record type (and per the
		// load-time mixed-mode prohibition, that means EVERY type on
		// the db is file-as-record) the backend is FileRecordBackend
		// instead of the heading-driven section backend.
		if schema.DBHasFileAsRecord(dbDecl) {
			fileType, st, ok := singleFileRecordType(dbDecl)
			if !ok {
				return nil, fmt.Errorf(
					"ops: db %q has file-as-record types but none resolved", dbDecl.Name,
				)
			}
			types := []record.DeclaredType{{Name: fileType}}
			b, err := md.NewFileRecordBackend(types, fileType, st.BodyField)
			if err != nil {
				return nil, fmt.Errorf("ops: build file-as-record backend for db %q: %w", dbDecl.Name, err)
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
			return nil, fmt.Errorf("ops: build MD backend for db %q: %w", dbDecl.Name, err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("%w: db %q format=%q", ErrUnsupportedFormat, dbDecl.Name, dbDecl.Format)
	}
}

// singleFileRecordType returns the one declared file-as-record type on
// dbDecl. Per F31's mixed-mode prohibition (load.go enforces) at most
// one such type can exist on a single db; this helper folds the lookup
// into one place. Returns (typeName, type, true) on hit, ("", zero,
// false) when no file-as-record type is declared.
func singleFileRecordType(dbDecl schema.DB) (string, schema.SectionType, bool) {
	for name, t := range dbDecl.Types {
		if t.IsFileRecord() {
			return name, t, true
		}
	}
	return "", schema.SectionType{}, false
}

// tomlScannerTypes returns the declared-prefix list for the TOML
// scanner under the prefix-anchor backend (single-file mounts only,
// post-F38d-2.15). For single-file dbs every record's bracket starts
// with `<file-relpath>.`, so we hand the scanner the file-relpath as
// its only prefix.
//
// Multi-file (glob) mounts no longer route through this function — see
// buildBackend, which constructs a NewTopLevelBracketBackend() for that
// branch. The fallback that returns declared type names is retained
// only for the degenerate non-single-file caller (no current
// production caller after F38d-2.15), guarding against accidental
// reuse before this helper's signature is collapsed.
func tomlScannerTypes(dbDecl schema.DB, resolved db.Resolved) []record.DeclaredType {
	if resolved.SingleFileMount {
		return []record.DeclaredType{{Name: resolved.FileRelPath}}
	}
	out := make([]record.DeclaredType, 0, len(dbDecl.Types))
	for typeName := range dbDecl.Types {
		out = append(out, record.DeclaredType{Name: typeName})
	}
	return out
}

// backendSectionPath converts the resolved id into the path shape
// each backend expects for Find/Emit/Splice. The two formats deliver
// asymmetric shapes by design (F30):
//
//   - FormatTOML: returns the on-disk bracket verbatim. Per F10 the
//     bracket IS the id; for single-file dbs the bracket starts with
//     the file-relpath, for multi-file dbs the bracket is the
//     bracket-key alone. bareType is unused on this branch.
//   - FormatMD: returns `<file-relpath>.<bareType>.<bracket-key>`.
//     The md backend's relativeAddress walks the section's dotted
//     segments left-to-right looking for the FIRST segment matching
//     a declared type-name; that segment anchors the relative
//     address it returns to the scanner's matcher. Pre-F10 sections
//     carried the type in the id; post-F10 the canonical id is
//     `<file-relpath>.<bracket-key>` and the type lives in the index.
//     Inserting bareType BETWEEN file-relpath and bracket-key
//     restores the shape relativeAddress expects: file-relpath
//     segments are stripped as qualifiers, bareType anchors, and
//     bracket-key becomes the chain that must match the scanner's
//     `<bareType>.<heading-slug-chain>` for a record at the H<level>
//     declared as bareType. For the canonical case where the
//     bracket-key IS the leaf heading slug, the chain length is 1
//     and the address shape equals `<bareType>.<bracket-key>`. A
//     more aspirational fix would refactor md.Backend to take type
//     as a separate Find/Emit/Splice parameter — that is an F31
//     follow-up; for F30 the asymmetric backendSectionPath is the
//     minimal-disruption surgical fix.
//
// bareType is the BARE type name (e.g. `agent`, not `agents.agent`)
// already resolved by ops via resolveTypeForID. Callers MUST resolve
// the type before invoking this function for MD-format dbs; an empty
// bareType on the MD branch produces a malformed section path that
// md.Backend.relativeAddress will reject as ErrNotDeclaredType.
func backendSectionPath(dbDecl schema.DB, resolved db.Resolved, bareType string) string {
	switch dbDecl.Format {
	case schema.FormatTOML:
		return tomlBracketPath(resolved)
	case schema.FormatMD:
		// Per F31: file-as-record dbs use the file-relpath as the
		// section path. There is no type-anchor / bracket-key chain to
		// re-thread because the whole file is the one record. The
		// FileRecordBackend ignores section semantics and returns the
		// whole-buffer range — but the caller still passes a non-empty
		// section so Find/Emit/Splice's empty-arg guard does not fire.
		if schema.DBHasFileAsRecord(dbDecl) {
			return resolved.FileRelPath
		}
		// MD addresses are type-anchored per md.Backend.relativeAddress;
		// insert bareType between file-relpath and bracket-key so the
		// canonical F10 id (which omits the type) parses against the
		// backend's declared-type table.
		parts := make([]string, 0, 3)
		if resolved.FileRelPath != "" {
			parts = append(parts, resolved.FileRelPath)
		}
		if bareType != "" && !strings.HasPrefix(resolved.BracketKey, bareType+".") {
			parts = append(parts, bareType)
		}
		if resolved.BracketKey != "" {
			parts = append(parts, resolved.BracketKey)
		}
		return strings.Join(parts, ".")
	default:
		return resolved.Canonical()
	}
}

// tomlBracketPath returns the on-disk TOML bracket path for resolved.
// Per F10 the bracket = id; for single-file dbs the id already
// includes the file-relpath, for multi-file dbs the bracket is the
// bracket-key alone.
func tomlBracketPath(resolved db.Resolved) string {
	if resolved.SingleFileMount {
		return resolved.Canonical()
	}
	return resolved.BracketKey
}
