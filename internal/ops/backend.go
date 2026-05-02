package ops

import (
	"fmt"

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
		types := tomlScannerTypes(dbDecl, resolved)
		return toml.NewBackend(types), nil
	case schema.FormatMD:
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

// tomlScannerTypes returns the declared-prefix list for the TOML
// scanner. For single-file dbs the scanner anchors at the file-relpath
// (every record's bracket starts with `<file-relpath>.`). For
// multi-file dbs the scanner anchors on declared type names.
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
// each backend expects for Find/Emit/Splice.
//
// Per F10 the on-disk bracket IS the id verbatim. For single-file
// dbs the bracket starts with the file-relpath; for multi-file dbs
// the bracket is the bracket-key alone (relative to its file).
func backendSectionPath(dbDecl schema.DB, resolved db.Resolved) string {
	switch dbDecl.Format {
	case schema.FormatTOML:
		return tomlBracketPath(resolved)
	case schema.FormatMD:
		// MD addresses are still type-anchored; pass the canonical id.
		return resolved.Canonical()
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
