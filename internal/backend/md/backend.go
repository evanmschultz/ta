package md

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/record"
)

// Backend is the record.Backend implementation for Markdown files.
//
// Per V2-PLAN §2.10 / §5.3.2 the MD scanner is schema-driven:
// NewBackend takes a slice of record.DeclaredType whose Heading fields
// identify which ATX heading levels count as record boundaries.
// Headings at non-declared levels are body content of the enclosing
// declared section — they do NOT split sections. Addresses compose as
// "<scope>.<type-name>.<slug>" where the type name is the Name of the
// DeclaredType whose Heading matches the heading level.
//
// A Backend is safe for concurrent use; it holds immutable schema
// information and is constructed fresh on every cascade reload. The
// zero value is NOT usable — always construct via NewBackend.
type Backend struct {
	types          []record.DeclaredType
	declaredLevels map[int]struct{}
	typeByLevel    map[int]string
}

// Compile-time assertion that *Backend satisfies record.Backend.
var _ record.Backend = (*Backend)(nil)

// NewBackend constructs a Backend aware of the declared types on the
// owning db (V2-PLAN §5.1). Each DeclaredType.Heading (1..6) marks an
// ATX heading level as a declared section boundary; its Name is the
// type name used when composing addresses for records at that level.
//
// Types sharing a heading level are a schema error and are reported as
// ErrDuplicateHeading. Types outside [1, 6] are ErrBadLevel. An empty
// types slice is legal and produces a Backend that recognizes no
// records (every heading is content).
func NewBackend(types []record.DeclaredType) (*Backend, error) {
	clone := make([]record.DeclaredType, len(types))
	copy(clone, types)

	levels := make(map[int]struct{}, len(clone))
	typeByLevel := make(map[int]string, len(clone))
	for _, t := range clone {
		if t.Heading < 1 || t.Heading > 6 {
			return nil, fmt.Errorf("%w: type %q has heading=%d", ErrBadLevel, t.Name, t.Heading)
		}
		if _, dup := levels[t.Heading]; dup {
			return nil, fmt.Errorf("%w: two types at heading=%d", ErrDuplicateHeading, t.Heading)
		}
		levels[t.Heading] = struct{}{}
		typeByLevel[t.Heading] = t.Name
	}
	return &Backend{
		types:          clone,
		declaredLevels: levels,
		typeByLevel:    typeByLevel,
	}, nil
}

// Types returns a copy of the declared types this backend was
// constructed with. Exposed for callers (the §12.3 resolver,
// diagnostics) that want to confirm which types a Backend serves.
func (b *Backend) Types() []record.DeclaredType {
	out := make([]record.DeclaredType, len(b.types))
	copy(out, b.types)
	return out
}

// List returns declared-section addresses under scope, in source
// order. Behavior:
//
//   - scope == "": returns "<type-name>.<slug>" for every declared
//     heading in buf. The caller (resolver) prepends "<db>" or
//     "<db>.<instance>" as the db shape requires.
//   - scope non-empty: treated as a filter prefix applied to the
//     "<type-name>.<slug>" address per declared heading. A heading's
//     address matches when it equals scope or starts with scope+".";
//     this mirrors the prefix semantics the higher layer uses for db /
//     type / id-prefix scopes.
//
// Non-declared headings are never returned — they are body content of
// the enclosing declared section (V2-PLAN §5.3.2). A Backend with no
// declared types always returns an empty slice.
func (b *Backend) List(buf []byte, scope string) ([]string, error) {
	hs, err := scanATX(buf, b.declaredLevels)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hs))
	prefix := scope + "."
	for _, h := range hs {
		typeName, ok := b.typeByLevel[h.Level]
		if !ok || typeName == "" {
			// Should not happen: scanATX filters by declared levels
			// already. Defensive skip.
			continue
		}
		addr := typeName + "." + h.Slug
		if scope == "" || addr == scope || strings.HasPrefix(addr, prefix) {
			out = append(out, addr)
		}
	}
	return out, nil
}

// Find locates one declared section by full address. The last
// dot-separated segment of section is treated as the heading slug; the
// segment immediately before it (when present) is matched against a
// declared type name to constrain the search to that type's heading
// level. If no declared type name matches the address tail, Find
// searches across all declared levels and returns the first match.
//
// Returns (section, true, nil) on hit; (zero, false, nil) when no
// declared heading matches; (zero, false, err) on parse errors such as
// slug collisions.
func (b *Backend) Find(buf []byte, section string) (record.Section, bool, error) {
	if section == "" {
		return record.Section{}, false, fmt.Errorf("%w", ErrEmptySection)
	}
	slug := lastSegment(section)
	if slug == "" {
		return record.Section{}, false, fmt.Errorf("%w: %q", ErrMalformedSection, section)
	}
	targetLevel := b.levelForAddress(section)
	hs, err := scanATX(buf, b.declaredLevels)
	if err != nil {
		return record.Section{}, false, err
	}
	for _, h := range hs {
		if targetLevel != 0 && h.Level != targetLevel {
			continue
		}
		if h.Slug == slug {
			return record.Section{
				Path:  section,
				Range: h.ByteRange,
			}, true, nil
		}
	}
	return record.Section{}, false, nil
}

// Emit serializes rec as a heading-plus-body block. The heading level
// is looked up from the declared types using the address's type-name
// segment; Emit errors with ErrNotDeclaredType when the address does
// not resolve to any declared type on this backend.
//
// The body-only field layout (V2-PLAN §5.3.3):
//
//   - Heading text is unslugified from the last segment of section
//     (e.g. "installation" → "Installation"). Lossy by design.
//   - Body is rec["body"] as a string; missing or empty body renders
//     the heading alone.
//   - A blank line separates heading from body when body is non-empty.
//   - Output always ends with exactly one '\n'.
func (b *Backend) Emit(section string, rec record.Record) ([]byte, error) {
	if section == "" {
		return nil, fmt.Errorf("%w", ErrEmptySection)
	}
	slug := lastSegment(section)
	if slug == "" {
		return nil, fmt.Errorf("%w: %q", ErrMalformedSection, section)
	}
	level := b.levelForAddress(section)
	if level == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNotDeclaredType, section)
	}
	heading := unslugifyForHeading(slug)

	var buf bytes.Buffer
	for i := 0; i < level; i++ {
		buf.WriteByte('#')
	}
	buf.WriteByte(' ')
	buf.WriteString(heading)
	buf.WriteByte('\n')

	body, _ := rec["body"].(string)
	if body != "" {
		buf.WriteByte('\n')
		buf.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes(), nil
}

// Splice replaces (or appends) a declared section's byte range in buf
// with emitted, preserving every byte outside the touched range
// verbatim. When the section does not yet exist, emitted is appended
// at EOF with a blank-line separator if needed (matching the TOML
// backend's append semantics).
//
// Byte-identity invariant: every byte of buf outside the replaced
// range is copied through unchanged. This matches V2-PLAN §5.1 and is
// exercised by FuzzSpliceInvariant. Non-declared headings that happen
// to live inside the touched declared section's body are replaced
// along with the rest of the body — they are body content, not
// sibling records (V2-PLAN §5.3.2).
func (b *Backend) Splice(buf []byte, section string, emitted []byte) ([]byte, error) {
	if section == "" {
		return nil, fmt.Errorf("%w", ErrEmptySection)
	}
	if len(emitted) == 0 {
		return nil, fmt.Errorf("md: splice: empty replacement")
	}

	sec, ok, err := b.Find(buf, section)
	if err != nil {
		return nil, err
	}

	rep := emitted
	if rep[len(rep)-1] != '\n' {
		rep = append(append([]byte{}, rep...), '\n')
	}

	if !ok {
		return b.appendSection(buf, rep), nil
	}

	out := make([]byte, 0, len(buf)+len(rep))
	out = append(out, buf[:sec.Range[0]]...)
	out = append(out, rep...)
	out = append(out, buf[sec.Range[1]:]...)
	return out, nil
}

func (b *Backend) appendSection(buf, rep []byte) []byte {
	if len(buf) == 0 {
		return append([]byte{}, rep...)
	}
	var sep []byte
	switch {
	case !bytes.HasSuffix(buf, []byte("\n")):
		sep = []byte("\n\n")
	case !bytes.HasSuffix(buf, []byte("\n\n")):
		sep = []byte("\n")
	}
	out := make([]byte, 0, len(buf)+len(sep)+len(rep))
	out = append(out, buf...)
	out = append(out, sep...)
	out = append(out, rep...)
	return out
}

// levelForAddress returns the declared heading level whose type-name
// suffix-matches the address (shape `...<type-name>.<slug>`). Returns
// 0 when no declared type matches — callers distinguish "unknown type"
// from "invalid address" via this zero return.
//
// Example: types = [{Name: "section", Heading: 2}], address =
// "readme.section.installation" → matches "section" → level 2.
//
// When types share the same prefix at different levels (e.g. Name
// "a.b" at H1 and "b" at H2 with address "x.a.b.slug"), the longest
// matching type-name wins so that more-specific declarations take
// precedence.
func (b *Backend) levelForAddress(section string) int {
	// Strip the last segment (slug) to get the address prefix whose
	// suffix must contain the type name.
	idx := strings.LastIndexByte(section, '.')
	if idx < 0 {
		return 0
	}
	prefix := section[:idx]
	bestLen := -1
	bestLevel := 0
	for _, t := range b.types {
		if t.Name == "" {
			continue
		}
		if prefix == t.Name || strings.HasSuffix(prefix, "."+t.Name) {
			if len(t.Name) > bestLen {
				bestLen = len(t.Name)
				bestLevel = t.Heading
			}
		}
	}
	return bestLevel
}

// lastSegment returns the substring after the last '.'. Empty when
// path does not contain '.'. (For slug extraction, we WANT the last
// segment — addresses are <db>.<type>.<slug> or
// <db>.<instance>.<type>.<slug>.)
func lastSegment(path string) string {
	idx := strings.LastIndexByte(path, '.')
	if idx < 0 {
		return ""
	}
	return path[idx+1:]
}

// unslugifyForHeading inverses (lossily) kebab-case: splits on '-',
// title-cases each segment, rejoins with a single space. This is
// deliberately cheap — humans do not round-trip heading text
// perfectly, and §5.3.3 names the loss.
func unslugifyForHeading(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
