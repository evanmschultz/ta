package ops

import (
	"fmt"

	"github.com/evanmschultz/ta/internal/backend/md"
	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// buildBackend constructs a record.Backend for the declared db, shaped
// per db format. Declared-type names are format-dependent because the
// data scanners anchor differently:
//
//   - TOML single-file mount: the on-disk file carries the db name in
//     the bracket path (e.g. `plans.toml` with `[plans.task.t1]`). So
//     the declared type prefix is "<db>.<type>" (e.g. "plans.task").
//
//   - TOML multi-file mount (glob or collection): the on-disk file
//     carries bare type brackets (e.g. `workflow/ta/db.toml` with
//     `[build_task.task_001]`). The declared type prefix is "<type>".
//
//   - MD (any shape): DeclaredType.Heading drives scanning; Name is the
//     bare type name ("section", "title"). Address stripping inside the
//     MD backend handles leading <db>[.<instance>] segments for us.
//
// `singleFile` is the resolved Address.SingleFileMount (or, for callers
// without an Address handy, schema.IsSingleFileDB(dbDecl)). It drives
// the TOML bracket-form choice — db-prefixed for single-file mounts,
// bare for everything else (PLAN §12.17.9 Phase 9.2).
func buildBackend(dbDecl schema.DB, singleFile bool) (record.Backend, error) {
	switch dbDecl.Format {
	case schema.FormatTOML:
		types := make([]record.DeclaredType, 0, len(dbDecl.Types))
		for typeName := range dbDecl.Types {
			prefix := tomlDeclaredName(dbDecl, typeName, singleFile)
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

// tomlDeclaredName returns the DeclaredType.Name the TOML backend
// expects given the resolved mount shape. Single-file mounts embed the
// db name in every bracket path on disk; multi-file mounts strip it
// (each file carries only "<type>.<id>"). The bracket-form choice keys
// off the resolved Address.SingleFileMount (PLAN §12.17.9 Phase 9.2),
// not the legacy IsSingleFile DB-shape view.
func tomlDeclaredName(dbDecl schema.DB, typeName string, singleFile bool) string {
	if singleFile {
		return dbDecl.Name + "." + typeName
	}
	return typeName
}

// backendSectionPath converts the resolved Address into the path shape
// each backend expects for Find/Emit/Splice.
//
// TOML wants the on-disk bracket form: db-prefixed for single-file
// mounts (`plans.task.t1`), bare for everything else (`build_task.t1`).
// tomlBracketPath builds that from the address fields directly so we
// no longer need to slice the input string.
//
// MD wants the full address verbatim — Backend.Find / Splice strip
// leading <db>[.<instance>] segments internally to find the type-name
// anchor. We pass the canonical address back so the strip-loop sees
// the same shape it always has.
func backendSectionPath(dbDecl schema.DB, addr db.Address) string {
	switch dbDecl.Format {
	case schema.FormatTOML:
		return tomlBracketPath(addr)
	case schema.FormatMD:
		return addr.Canonical()
	default:
		return addr.Canonical()
	}
}

// tomlBracketPath builds the on-disk TOML bracket path for addr. Mirrors
// the buildBackend bracket-form rule: db-prefixed when the resolved
// mount is single-file, bare otherwise.
//
//	addr.SingleFileMount == true   →  "<db>.<type>.<id>"
//	addr.SingleFileMount == false  →  "<type>.<id>"
//
// Both forms collapse the trailing `.<id>` segment when addr.ID is
// empty (scope-prefix addresses) so the helper round-trips with the
// scanner's path equality check.
func tomlBracketPath(addr db.Address) string {
	base := addr.Type
	if addr.ID != "" {
		base += "." + addr.ID
	}
	if addr.SingleFileMount {
		return addr.DBName + "." + base
	}
	return base
}
