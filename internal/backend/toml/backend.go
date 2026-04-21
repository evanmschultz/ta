package toml

import (
	"fmt"

	"github.com/evanmschultz/ta/internal/record"
)

// Backend is the record.Backend implementation for TOML-backed files. It
// wraps the package's stateless scanner + emitter + splicer so the
// lang-agnostic layer above (schema resolution, validation, search, MCP
// routing) can drive TOML work through the record.Backend seam without
// reaching for package-level functions.
//
// The zero value is ready to use; there is no per-backend state.
type Backend struct{}

// NewBackend returns a Backend value. Exposed for call sites that prefer
// constructor syntax; equivalent to Backend{}.
func NewBackend() Backend { return Backend{} }

// Compile-time assertion that *Backend satisfies record.Backend.
var _ record.Backend = (*Backend)(nil)

// List returns every section path under scope. When scope is the empty
// string every section in buf is returned. When scope is non-empty, only
// sections whose full path equals scope or starts with scope + "." are
// returned; this mirrors the prefix semantics used by the higher layer
// for db/type-scoped queries.
//
// Sections are returned in source order.
func (Backend) List(buf []byte, scope string) ([]string, error) {
	f, err := ParseBytes("", buf)
	if err != nil {
		return nil, err
	}
	all := f.Paths()
	if scope == "" {
		return all, nil
	}
	prefix := scope + "."
	out := make([]string, 0, len(all))
	for _, p := range all {
		if p == scope || len(p) > len(prefix) && p[:len(prefix)] == prefix {
			out = append(out, p)
		}
	}
	return out, nil
}

// Find locates one section by its full path. It returns the record.Section
// view of the located section (with Record left nil — this backend is
// locator-only for now; field decoding belongs to a higher layer). The
// returned bool is false when no section matches the path; err is non-nil
// only for parse failures on buf.
func (Backend) Find(buf []byte, section string) (record.Section, bool, error) {
	if section == "" {
		return record.Section{}, false, fmt.Errorf("find: empty section path")
	}
	f, err := ParseBytes("", buf)
	if err != nil {
		return record.Section{}, false, err
	}
	s, ok := f.Find(section)
	if !ok {
		return record.Section{}, false, nil
	}
	return record.Section{
		Path:  s.Path,
		Range: s.Range,
	}, true, nil
}

// Emit serializes rec to canonical TOML bytes under the given section
// path. Delegates to EmitSection.
func (Backend) Emit(section string, rec record.Record) ([]byte, error) {
	return EmitSection(section, map[string]any(rec))
}

// Splice replaces (or appends) the named section's bytes in buf with
// emitted, preserving every byte outside the touched range verbatim.
// Delegates to (*File).Splice after parsing buf.
func (Backend) Splice(buf []byte, section string, emitted []byte) ([]byte, error) {
	f, err := ParseBytes("", buf)
	if err != nil {
		return nil, err
	}
	return f.Splice(section, emitted)
}
