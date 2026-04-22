package md

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/record"
)

// Backend is the record.Backend implementation for Markdown files.
//
// Unlike the stateless TOML backend, Backend carries the heading level
// (1..6) of the record type it serves. MD sections are identified by
// heading level, so Emit needs to know the level to render the correct
// number of '#' chars. Callers construct one Backend per record type:
// readme.title → level 1, readme.section → level 2, etc.
type Backend struct {
	level int
}

// Compile-time assertion that *Backend satisfies record.Backend.
var _ record.Backend = (*Backend)(nil)

// NewBackend constructs a Backend bound to heading level. Returns
// ErrBadLevel when level is outside [1, 6].
func NewBackend(level int) (*Backend, error) {
	if level < 1 || level > 6 {
		return nil, fmt.Errorf("%w: got %d", ErrBadLevel, level)
	}
	return &Backend{level: level}, nil
}

// Level returns the heading level this Backend is bound to. Exposed
// for callers (the §12.3 resolver, diagnostics) that want to confirm
// which level a Backend serves.
func (b *Backend) Level() int { return b.level }

// List returns section paths under scope. Semantics:
//
//   - scope == "": returns synthetic locators "H<level>.<slug>" for
//     every heading in buf. The caller (resolver) translates these to
//     concrete <db>.<type>.<slug> addresses using its schema knowledge.
//     Only headings matching b.level are included when the caller does
//     not provide scope context — the backend cannot know which
//     heading-level a type binds without schema input.
//
// Wait — on second read: scope == "" should emit synthetic locators
// across ALL levels so a caller that wants to enumerate everything gets
// everything. Level filtering happens when scope is non-empty.
//
//   - scope non-empty: treated as the type prefix "<db>.<type>" (or
//     "<db>.<instance>.<type>" for multi-instance callers). The
//     backend appends ".<slug>" for each heading whose level matches
//     b.level. Sections whose level does not match are filtered out.
//
// Sections are returned in source order.
//
// NOTE on Path stitching: the backend does not know the db or type
// name. The scope argument carries the stitching prefix, so the caller
// is responsible for providing a meaningful prefix. When the caller is
// the §12.3 resolver that knows the type maps to b.level, this is
// straightforward. The empty-scope case returns synthetic locators
// that the resolver can map back to concrete addresses.
func (b *Backend) List(buf []byte, scope string) ([]string, error) {
	hs, err := scanATX(buf)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hs))
	if scope == "" {
		for _, h := range hs {
			out = append(out, fmt.Sprintf("H%d.%s", h.Level, h.Slug))
		}
		return out, nil
	}
	for _, h := range hs {
		if h.Level != b.level {
			continue
		}
		out = append(out, scope+"."+h.Slug)
	}
	return out, nil
}

// Find locates one section by full address. The last dot-separated
// segment of section is treated as the heading slug; the backend
// matches at b.level.
//
// Returns (section, true, nil) on hit; (zero, false, nil) when no
// heading at this level matches the slug; (zero, false, err) on
// parse errors such as slug collisions.
func (b *Backend) Find(buf []byte, section string) (record.Section, bool, error) {
	if section == "" {
		return record.Section{}, false, fmt.Errorf("%w", ErrEmptySection)
	}
	slug := lastSegment(section)
	if slug == "" {
		return record.Section{}, false, fmt.Errorf("%w: %q", ErrMalformedSection, section)
	}
	hs, err := scanATX(buf)
	if err != nil {
		return record.Section{}, false, err
	}
	for _, h := range hs {
		if h.Level != b.level {
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

// Emit serializes rec as a heading-plus-body block for this backend's
// heading level. The body-only field layout (§5.3.3):
//
//   - Heading text is unslugified from the last segment of section
//     (e.g. "installation" → "Installation",
//     "mcp-client-config" → "Mcp Client Config"). This is lossy by
//     design; MD authors use `update` if they want to change heading
//     text, and §5.3.3 documents the accepted loss.
//   - Body is rec["body"] as a string; missing or empty body still
//     renders the heading alone.
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
	heading := unslugifyForHeading(slug)

	var buf bytes.Buffer
	for i := 0; i < b.level; i++ {
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

// Splice replaces the named section's byte range in buf with emitted,
// preserving every byte outside the touched range verbatim. If the
// section does not yet exist in buf, emitted is appended at EOF with
// a blank-line separator if needed (matching the TOML backend's
// append semantics).
//
// Byte-identity invariant: every byte of buf outside the replaced
// range is copied through unchanged. This matches V2-PLAN §5.1 and is
// exercised by FuzzSpliceInvariant.
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
