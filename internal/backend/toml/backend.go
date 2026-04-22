package toml

import (
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/record"
)

// Backend is the record.Backend implementation for TOML-backed files.
//
// Per V2-PLAN §2.10 / §5.2 the TOML scanner is schema-driven: after the
// low-level pelletier-compatible scanner enumerates every bracket in the
// file, the Backend filters that raw list down to brackets whose path
// equals or starts with ("." separator) one of the declared-type
// prefixes it was constructed with. Brackets that don't match any
// declared prefix are content of the preceding declared record — their
// bytes live inside the record's body range, they do not become
// sibling records.
//
// Body range for a declared record extends from its header line to the
// start of the NEXT declared bracket (at any declared type) or EOF,
// matching V2-PLAN §2.11. This is what lets a declared record's body
// carry a non-declared bracket like "[plans.task.t1.notes]" without
// that bracket becoming a sibling record.
//
// Zero value is NOT usable — always construct via NewBackend. A Backend
// with an empty types slice treats no bracket as a declared record, so
// List returns an empty slice and Find returns not-found for every
// input. That matches the spec: a backend without declared types has
// no records to enumerate.
type Backend struct {
	types []record.DeclaredType
}

// NewBackend constructs a TOML Backend aware of the declared types on
// the owning db (V2-PLAN §5.1). Each DeclaredType.Name is treated as a
// bracket-path prefix: a bracket whose path equals Name or starts with
// Name+"." is a declared record. DeclaredType.Heading is unused for
// this backend.
//
// Passing nil or an empty slice is legal — the resulting Backend
// reports no records. Callers rebuild the Backend when the resolved
// schema cascade reloads.
func NewBackend(types []record.DeclaredType) Backend {
	// Defensive copy so callers cannot mutate our view of the schema.
	clone := make([]record.DeclaredType, len(types))
	copy(clone, types)
	return Backend{types: clone}
}

// Compile-time assertion that Backend satisfies record.Backend.
var _ record.Backend = Backend{}

// isDeclared reports whether bracket path p matches any declared-type
// prefix on this backend at the right anchor depth. Per V2-PLAN §5.2:
//
//   - A bracket whose path equals Name exactly is a declared record.
//   - A bracket whose path is Name + "." + <exactly-one-segment> is a
//     declared record.
//   - A bracket whose path has MORE than one segment past Name (e.g.
//     Name="plans.task", bracket "plans.task.t1.notes") is NOT a
//     declared record — it is body content of the enclosing declared
//     record. This is what lets a TOML record's body carry a nested
//     "[plans.task.t1.notes]" bookkeeping bracket without promoting it
//     to a sibling record.
//
// If a caller wants deeper brackets to be records, the schema must
// declare another type at the deeper anchor (e.g. `[plans.notes]`)
// per the §5.2 worked example.
func (b Backend) isDeclared(p string) bool {
	for _, t := range b.types {
		if t.Name == "" {
			continue
		}
		if p == t.Name {
			return true
		}
		if strings.HasPrefix(p, t.Name+".") {
			tail := p[len(t.Name)+1:]
			if tail != "" && !strings.ContainsRune(tail, '.') {
				return true
			}
		}
	}
	return false
}

// declaredSections returns, in source order, the parsed sections whose
// bracket paths are declared per b.types. It also returns the parsed
// File so callers can reuse the section list without re-parsing.
func (b Backend) declaredSections(buf []byte) (*File, []Section, error) {
	f, err := ParseBytes("", buf)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Section, 0, len(f.Sections))
	for _, s := range f.Sections {
		if b.isDeclared(s.Path) {
			out = append(out, s)
		}
	}
	return f, out, nil
}

// declaredRange computes the body byte range of a declared record under
// the schema-driven rule (§2.11): from the record's header line (after
// any leading comment block that belongs to it) to the start of the
// next declared bracket, or EOF for the last declared record. Non-
// declared brackets between the two are absorbed into the body.
//
// For Splice the returned range's start is the HeaderRange.Start of the
// declared section; its end is leadStart of the next declared section
// (so that the next declared record's leading comment block survives
// the splice intact).
func declaredRange(buf []byte, declared []Section, idx int) [2]int {
	start := declared[idx].HeaderRange[0]
	var end int
	if idx+1 < len(declared) {
		end = declared[idx+1].HeadRange[0]
	} else {
		end = len(buf)
	}
	return [2]int{start, end}
}

// List returns every declared bracket path under scope, in source
// order. When scope == "" every declared bracket is returned.
// Otherwise a bracket matches when it equals scope or starts with
// scope+"."  — prefix semantics the higher layer uses for db / type
// scoped queries.
//
// Non-declared brackets in the buffer are never returned. They are
// content of the enclosing declared record per V2-PLAN §2.10.
func (b Backend) List(buf []byte, scope string) ([]string, error) {
	_, declared, err := b.declaredSections(buf)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(declared))
	prefix := scope + "."
	for _, s := range declared {
		if scope == "" || s.Path == scope || strings.HasPrefix(s.Path, prefix) {
			out = append(out, s.Path)
		}
	}
	return out, nil
}

// Find locates one declared bracket by its full path. It returns the
// record.Section view with Record left nil — this backend is
// locator-only; field decoding is a higher-layer concern. The returned
// bool is false when no declared bracket matches the path. err is
// non-nil only for parse failures on buf.
//
// A bracket that exists in the file but is not declared returns
// (zero, false, nil) — non-declared brackets are content of the
// enclosing declared record, not records in their own right.
func (b Backend) Find(buf []byte, section string) (record.Section, bool, error) {
	if section == "" {
		return record.Section{}, false, fmt.Errorf("toml: find: empty section path")
	}
	_, declared, err := b.declaredSections(buf)
	if err != nil {
		return record.Section{}, false, err
	}
	for i, s := range declared {
		if s.Path == section {
			return record.Section{
				Path:  s.Path,
				Range: declaredRange(buf, declared, i),
			}, true, nil
		}
	}
	return record.Section{}, false, nil
}

// Emit serializes rec to canonical TOML bytes under the given section
// path. Delegates to EmitSection. Declared-ness is not verified here —
// callers supply the section path they intend to write and the Backend
// trusts them (the higher layer validates against schema before
// calling Emit). This keeps Emit byte-for-byte compatible with the
// pre-refactor behavior.
func (b Backend) Emit(section string, rec record.Record) ([]byte, error) {
	return EmitSection(section, map[string]any(rec))
}

// Splice replaces (or appends) a declared record's bytes in buf,
// preserving every byte outside the touched range verbatim. Under the
// schema-driven model (§2.11) the touched range runs from the declared
// section's header line to the start of the NEXT declared section (or
// EOF). A non-declared bracket that happened to live inside that range
// is absorbed into the body and therefore gets replaced too; the
// caller's Emit output is the new full body for this declared record.
//
// When the section does not yet exist, the emitted bytes are appended
// to the buffer with a blank-line separator if needed, same as the
// pre-refactor append semantics.
func (b Backend) Splice(buf []byte, section string, emitted []byte) ([]byte, error) {
	if section == "" {
		return nil, fmt.Errorf("toml: splice: empty section path")
	}
	if len(emitted) == 0 {
		return nil, fmt.Errorf("toml: splice: empty replacement")
	}
	f, declared, err := b.declaredSections(buf)
	if err != nil {
		return nil, err
	}
	rep := emitted
	if rep[len(rep)-1] != '\n' {
		rep = append(append([]byte{}, rep...), '\n')
	}
	for i, s := range declared {
		if s.Path == section {
			rng := declaredRange(buf, declared, i)
			out := make([]byte, 0, len(buf)+len(rep))
			out = append(out, buf[:rng[0]]...)
			out = append(out, rep...)
			out = append(out, buf[rng[1]:]...)
			return out, nil
		}
	}
	// Append when the declared bracket is absent. Reuse the low-level
	// File append logic so the blank-line-separator heuristic stays in
	// one place.
	return f.appendSection(rep), nil
}
