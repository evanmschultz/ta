package ops

import (
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/backend/md"
	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// buildBackend constructs a record.Backend for the declared db, shaped
// per db format. Declared-type names are format-dependent because the
// data scanners anchor differently:
//
//   - TOML legacy single-file db: the on-disk file carries the db name in
//     the bracket path (e.g. `plans.toml` with `[plans.task.t1]`). So
//     the declared type prefix is "<db>.<type>" (e.g. "plans.task").
//
//   - TOML legacy multi-instance db (dir-per-instance, collection): the
//     on-disk file is inside an instance dir/file and carries bare type
//     brackets (e.g. `workflow/ta/db.toml` with
//     `[build_task.task_001]`). The declared type prefix is "<type>".
//
//   - MD (any shape): DeclaredType.Heading drives scanning; Name is the
//     bare type name ("section", "title"). Address stripping inside the
//     MD backend handles leading <db>[.<instance>] segments for us.
//
// TODO(PLAN §12.17.9 Phase 9.4): rewire on the new address grammar.
func buildBackend(dbDecl schema.DB) (record.Backend, error) {
	switch dbDecl.Format {
	case schema.FormatTOML:
		types := make([]record.DeclaredType, 0, len(dbDecl.Types))
		for typeName := range dbDecl.Types {
			prefix := tomlDeclaredName(dbDecl, typeName)
			types = append(types, record.DeclaredType{Name: prefix})
		}
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

// tomlDeclaredName returns the DeclaredType.Name the TOML backend expects
// given the db shape. Legacy single-file dbs embed the db name in every
// bracket path on disk; multi-instance dbs strip it (each instance file
// carries only "<type>.<id>"). Covers both dir-per-instance and
// collection shapes as a single rule because both put the file under an
// instance identity and both emit bare type brackets.
func tomlDeclaredName(dbDecl schema.DB, typeName string) string {
	if schema.IsSingleFile(dbDecl) {
		return dbDecl.Name + "." + typeName
	}
	return typeName
}

// backendSectionPath strips the leading <db> (single-instance) or
// <db>.<instance> (multi-instance) qualifiers from a full address to
// produce the path shape each backend expects for Find/Emit/Splice.
//
// MD backends already handle the strip internally (relativeAddress
// walks segments to find the first declared type name), so returning
// the address unchanged for MD is safe too. This helper is load-bearing
// only for TOML, which matches declared-prefix substrings exactly.
func backendSectionPath(dbDecl schema.DB, section string) string {
	switch dbDecl.Format {
	case schema.FormatTOML:
		return stripTOMLPrefix(dbDecl, section)
	case schema.FormatMD:
		// The MD backend handles <db>[.<instance>] prefixes itself.
		return section
	default:
		return section
	}
}

// stripTOMLPrefix removes the db + optional instance prefix from the
// address, leaving the "<type>.<id>" path the file-scoped TOML backend
// expects. For a legacy single-file db the prefix is just "<db>."; for
// multi-instance it is "<db>.<instance>.".
func stripTOMLPrefix(dbDecl schema.DB, section string) string {
	if schema.IsSingleFile(dbDecl) {
		// Legacy single-file: backend declared with "<db>.<type>" prefix,
		// so the section path already matches on-disk brackets verbatim.
		return section
	}
	// Multi-instance: strip "<db>.<instance>." — two leading segments.
	parts := strings.SplitN(section, ".", 3)
	if len(parts) < 3 {
		return section
	}
	return parts[2]
}
