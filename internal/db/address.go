package db

import (
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/schema"
)

// Address is the structured view of a dotted section path. DB and Type
// are always populated on a successful parse; Instance is empty for
// legacy single-file dbs and populated for multi-instance dbs. ID is the
// remainder after the <type> segment joined with '.' — empty for
// address forms that stop at the type (not currently accepted by
// ParseAddress, but reserved for future enum-like callers).
type Address struct {
	DB       string
	Instance string
	Type     string
	ID       string
}

// Canonical returns the round-trippable dotted-string form of addr.
// Empty Instance is omitted (single-instance shape); empty ID trims
// the trailing dot.
func (a Address) Canonical() string {
	var parts []string
	parts = append(parts, a.DB)
	if a.Instance != "" {
		parts = append(parts, a.Instance)
	}
	parts = append(parts, a.Type)
	if a.ID != "" {
		parts = append(parts, a.ID)
	}
	return strings.Join(parts, ".")
}

// ParseAddress splits section into an Address shaped to the resolved db.
// Returns ErrUnknownDB for an unrecognised first segment, ErrUnknownType
// when the type segment is not declared on the db, and ErrBadAddress
// when the segment count is below the minimum for the db's shape or an
// intermediate segment is empty (§5.5 "tools resolve which form applies
// by looking up the db's declaration").
//
// The grammar is uniform across formats (§2.9, §11.D) — Phase 9.1
// (PLAN §12.17.9) keeps the pre-9.1 forms intact, branching on
// schema.IsSingleFile to pick between them. Phase 9.2 replaces this
// parser with the new no-db-prefix grammar `<file-relpath>.<id-tail>`.
//
//   - legacy single-file: "<db>.<type>.<id-path>" with len(parts) >= 3.
//   - legacy multi-instance: "<db>.<instance>.<type>.<id-path>" with
//     len(parts) >= 4.
//
// <id-path> is one or more dot-separated segments, joined with '.' into
// addr.ID so deep TOML bracket paths (e.g. "plans.task.t1.subtask") and
// single-segment MD slugs (e.g. "installation") both round-trip through
// the same rule.
//
// The returned schema.DB is the resolved db declaration; callers that
// need the type descriptor can look it up on db.Types[addr.Type].
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

	dbDecl, ok := r.registry.DBs[parts[0]]
	if !ok {
		return Address{}, schema.DB{}, fmt.Errorf("%w: %q", ErrUnknownDB, parts[0])
	}

	var addr Address
	addr.DB = parts[0]

	// TODO(PLAN §12.17.9 Phase 9.2): replace IsSingleFile branch with the
	// new paths-glob-aware grammar. Phase 9.1 preserves the pre-9.1
	// segment-count rules so downstream packages compile during the
	// transitional window.
	if schema.IsSingleFile(dbDecl) {
		// <db>.<type>.<id-path>, 3+ segments; tail joined with '.'.
		if len(parts) < 3 {
			return Address{}, schema.DB{}, fmt.Errorf(
				"%w: single-instance db %q needs <db>.<type>.<id-path>, got %q",
				ErrBadAddress, dbDecl.Name, section)
		}
		addr.Type = parts[1]
		addr.ID = strings.Join(parts[2:], ".")
	} else {
		// <db>.<instance>.<type>.<id-path>, 4+ segments; tail joined with '.'.
		if len(parts) < 4 {
			return Address{}, schema.DB{}, fmt.Errorf(
				"%w: multi-instance db %q needs <db>.<instance>.<type>.<id-path>, got %q",
				ErrBadAddress, dbDecl.Name, section)
		}
		addr.Instance = parts[1]
		addr.Type = parts[2]
		addr.ID = strings.Join(parts[3:], ".")
	}

	if _, ok := dbDecl.Types[addr.Type]; !ok {
		return Address{}, schema.DB{}, fmt.Errorf("%w: %q on db %q",
			ErrUnknownType, addr.Type, dbDecl.Name)
	}

	return addr, dbDecl, nil
}
