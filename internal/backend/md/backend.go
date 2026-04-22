package md

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/evanmschultz/ta/internal/record"
)

// Backend is the record.Backend implementation for Markdown files.
//
// Per V2-PLAN §2.10 / §5.3.2 the MD scanner is schema-driven:
// NewBackend takes a slice of record.DeclaredType whose Heading fields
// identify which ATX heading levels count as record boundaries.
// Headings at non-declared levels are body content of the enclosing
// declared section — they do NOT split sections.
//
// Addresses compose hierarchically (2026-04-21 refinement): a record
// at declared level N is addressed as "<type-name>.<chain>" where
// <chain> is the ordered slugs of declared-level headings from the
// shallowest declared level down to this heading (inclusive). A bare
// H2 section under schema {H1 title, H2 section} resolves to
// "section.<h1-slug>.<h2-slug>"; if no H1 type were declared, the chain
// would start at H2 → "section.<h2-slug>". See scanATX doc for the full
// stack-machine rules including orphan-heading handling.
//
// A Backend is safe for concurrent use; it holds immutable schema
// information and is constructed fresh on every cascade reload. The
// zero value is NOT usable — always construct via NewBackend.
type Backend struct {
	types          []record.DeclaredType
	typeByLevel    map[int]string
	levelByType    map[string]int
	declaredSorted []int
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

	typeByLevel := make(map[int]string, len(clone))
	levelByType := make(map[string]int, len(clone))
	for _, t := range clone {
		if t.Heading < 1 || t.Heading > 6 {
			return nil, fmt.Errorf("%w: type %q has heading=%d", ErrBadLevel, t.Name, t.Heading)
		}
		if _, dup := typeByLevel[t.Heading]; dup {
			return nil, fmt.Errorf("%w: two types at heading=%d", ErrDuplicateHeading, t.Heading)
		}
		typeByLevel[t.Heading] = t.Name
		levelByType[t.Name] = t.Heading
	}
	declaredSorted := make([]int, 0, len(typeByLevel))
	for lvl := range typeByLevel {
		declaredSorted = append(declaredSorted, lvl)
	}
	sort.Ints(declaredSorted)
	return &Backend{
		types:          clone,
		typeByLevel:    typeByLevel,
		levelByType:    levelByType,
		declaredSorted: declaredSorted,
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
//   - scope == "": returns the full hierarchical address
//     "<type-name>.<chain>" for every declared heading in buf. The
//     caller (resolver) prepends "<db>" or "<db>.<instance>" as the db
//     shape requires.
//   - scope non-empty: treated as a segment-aligned prefix filter
//     applied to each full address. A heading matches when its address
//     equals scope or starts with scope+".". Descendants of a matching
//     scope are included — e.g. scope "section.install" returns
//     "section.install" PLUS "section.install.<subslug>" at the same
//     declared type.
//
// Scope targets one type at a time — records under a different declared
// type live in a different "<type-name>.…" namespace. To enumerate
// subsection records under "section.install", scope
// "subsection.install" (the declared type name of the deeper level).
// See V2-PLAN §11.D pre-answered ambiguity #4.
//
// Non-declared headings are never returned (body content of the
// enclosing declared section). A Backend with no declared types always
// returns an empty slice.
func (b *Backend) List(buf []byte, scope string) ([]string, error) {
	hs, err := scanATX(buf, b.typeByLevel)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hs))
	prefix := scope + "."
	for _, h := range hs {
		if scope == "" || h.Address == scope || strings.HasPrefix(h.Address, prefix) {
			out = append(out, h.Address)
		}
	}
	return out, nil
}

// Find locates one declared section by full address. The section
// argument may carry leading "<db>" or "<db>.<instance>" qualifiers —
// Find strips any leading segments until it finds the declared
// type-name that anchors the rest of the address, then matches the
// full chain suffix against scanned addresses.
//
// Returns (section, true, nil) on hit; (zero, false, nil) when no
// declared heading matches; (zero, false, err) on parse errors such as
// address collisions.
func (b *Backend) Find(buf []byte, section string) (record.Section, bool, error) {
	if section == "" {
		return record.Section{}, false, fmt.Errorf("%w", ErrEmptySection)
	}
	rel, ok := b.relativeAddress(section)
	if !ok {
		return record.Section{}, false, nil
	}
	hs, err := scanATX(buf, b.typeByLevel)
	if err != nil {
		return record.Section{}, false, err
	}
	for _, h := range hs {
		if h.Address == rel {
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
//   - Heading text is unslugified from the LAST segment of section
//     (e.g. "installation" → "Installation"). Lossy by design.
//   - Body is rec["body"] as a string; missing or empty body renders
//     the heading alone.
//   - A blank line separates heading from body when body is non-empty.
//   - Output always ends with exactly one '\n'.
func (b *Backend) Emit(section string, rec record.Record) ([]byte, error) {
	if section == "" {
		return nil, fmt.Errorf("%w", ErrEmptySection)
	}
	rel, ok := b.relativeAddress(section)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotDeclaredType, section)
	}
	slug := lastSegment(rel)
	if slug == "" {
		return nil, fmt.Errorf("%w: %q", ErrMalformedSection, section)
	}
	level := b.levelForRelative(rel)
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

// Splice replaces (or inserts) a declared section's byte range in buf
// with emitted, preserving every byte outside the touched range
// verbatim.
//
// Cases (V2-PLAN §5.3.2 / §11.D #3, 2026-04-21 refinement):
//
//   - Section exists: byte-range replacement at the located range.
//     Deeper declared headings that were nested inside the replaced
//     range are removed along with the rest of the body; callers that
//     wish to preserve children must include them in emitted.
//   - Section does not exist, address has no declared ancestor (chain
//     length 1): emitted is appended at EOF with a blank-line separator
//     if needed.
//   - Section does not exist, declared parent EXISTS in buf: emitted is
//     inserted at the end of the parent's body range (just before the
//     next same-or-shallower declared heading, or EOF). This mirrors
//     the TOML "insert nested child between parent and parent's end"
//     rule.
//   - Section does not exist, declared parent also absent: returns
//     ErrParentMissing. The caller must create the parent first;
//     silent auto-creation would hide typos.
func (b *Backend) Splice(buf []byte, section string, emitted []byte) ([]byte, error) {
	if section == "" {
		return nil, fmt.Errorf("%w", ErrEmptySection)
	}
	if len(emitted) == 0 {
		return nil, fmt.Errorf("md: splice: empty replacement")
	}

	rel, ok := b.relativeAddress(section)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotDeclaredType, section)
	}
	// Splice must accept exactly the addresses Emit accepts: a bare
	// type segment with no slug (e.g. "readme.title") is malformed
	// regardless of whether the type exists. Guard matches Emit at
	// line 180-183 so the two entry points share one contract.
	if lastSegment(rel) == "" {
		return nil, fmt.Errorf("%w: %q", ErrMalformedSection, section)
	}

	hs, err := scanATX(buf, b.typeByLevel)
	if err != nil {
		return nil, err
	}

	rep := emitted
	if rep[len(rep)-1] != '\n' {
		rep = append(append([]byte{}, rep...), '\n')
	}

	// Replace-existing.
	for _, h := range hs {
		if h.Address == rel {
			out := make([]byte, 0, len(buf)+len(rep))
			out = append(out, buf[:h.ByteRange[0]]...)
			out = append(out, rep...)
			out = append(out, buf[h.ByteRange[1]:]...)
			return out, nil
		}
	}

	// Missing: locate parent for insertion or report ErrParentMissing.
	parentAddr, hasParent := b.parentAddress(rel)
	if !hasParent {
		// Top-of-chain record with no declared ancestor — append at EOF.
		return b.appendSection(buf, rep), nil
	}
	for _, h := range hs {
		if h.Address == parentAddr {
			// Insert at the end of parent's body range.
			return b.insertAt(buf, h.ByteRange[1], rep), nil
		}
	}
	return nil, fmt.Errorf("%w: %q missing while splicing %q", ErrParentMissing, parentAddr, section)
}

// relativeAddress strips any leading "<db>" or "<db>.<instance>"
// qualifiers from section and returns the "<type-name>.<chain>"
// relative address the scanner and addresses produce. Returns ok=false
// when section contains no segment that matches a declared type name.
//
// Addresses are segment-aligned: a declared type name "section" only
// matches when the preceding character (if any) is '.'. This prevents
// false matches against arbitrary substrings.
func (b *Backend) relativeAddress(section string) (string, bool) {
	// Scan segment boundaries left-to-right. The first segment whose
	// token equals a declared type name anchors the relative address.
	// Ties (e.g. a db name equal to a type name) are resolved by
	// leftmost-first — the db qualifier is always before the type in
	// well-formed addresses.
	segs := strings.Split(section, ".")
	for i, seg := range segs {
		if _, ok := b.levelByType[seg]; ok {
			return strings.Join(segs[i:], "."), true
		}
	}
	return "", false
}

// levelForRelative returns the declared level of the type prefix of a
// RELATIVE address (as returned by relativeAddress). The relative
// address shape is "<type-name>.<chain...>"; the first segment is the
// type name.
func (b *Backend) levelForRelative(rel string) int {
	dot := strings.IndexByte(rel, '.')
	if dot < 0 {
		return 0
	}
	return b.levelByType[rel[:dot]]
}

// parentAddress derives the declared-parent relative address of rel
// (V2-PLAN §5.3.2 hierarchical addressing). For "<type>.<chain>" where
// chain has length K>1, the parent is "<parent-type>.<chain[:K-1]>"
// and the parent-type is the declared type at the deepest declared
// heading level strictly shallower than self — REGARDLESS of whether
// that level's slug is present in the chain the scanner produced.
//
// Returns ok=false for top-of-chain addresses (chain length 1 — no
// declared ancestor).
//
// Strict-orphan write semantics (V2-PLAN §5.3.2 orphans paragraph).
// When the buffer has an orphan chain — e.g. an H3 under an H1 when
// H1+H2+H3 are all declared but no H2 sits between — Splice of a new
// sibling at that orphan level fails with ErrParentMissing. This
// function computes the parent address as "<section-type>.<h1-slug>"
// (the next-shallower declared level, H2 "section"), the scanner does
// not find that heading in the buffer, and Splice returns the sentinel.
//
// This is intentional: existing orphan records remain readable (the
// scanner READ path does not call parentAddress), but agent-authored
// WRITES must materialize the missing declared ancestor first — the
// caller creates the H2, then the H3 Splice succeeds. Fail-loudly
// behavior per V2-PLAN §1.1 / §2.10; keeps tool-authored output
// schema-consistent.
func (b *Backend) parentAddress(rel string) (string, bool) {
	segs := strings.Split(rel, ".")
	if len(segs) < 2 {
		return "", false
	}
	selfType := segs[0]
	chain := segs[1:]
	if len(chain) < 2 {
		// Chain of length 1 — record has no declared ancestor.
		return "", false
	}
	// Self's declared level.
	selfLevel := b.levelByType[selfType]
	if selfLevel == 0 {
		return "", false
	}
	// Parent-type = declared type at the deepest level strictly shallower
	// than selfLevel. Parent chain = chain[:len(chain)-1].
	var parentLevel int
	for _, dl := range b.declaredSorted {
		if dl < selfLevel {
			parentLevel = dl
		}
	}
	if parentLevel == 0 {
		return "", false
	}
	parentType := b.typeByLevel[parentLevel]
	parentChain := chain[:len(chain)-1]
	return parentType + "." + strings.Join(parentChain, "."), true
}

// insertAt returns a new buffer with rep inserted at byte offset pos.
// Ensures a blank-line separator precedes rep when the preceding bytes
// do not already provide one AND a blank-line separator follows rep
// when the byte at pos is additional non-empty content, so the
// inserted heading starts on its own line and does not butt against a
// subsequent heading line.
func (b *Backend) insertAt(buf []byte, pos int, rep []byte) []byte {
	var preSep []byte
	if pos > 0 {
		switch {
		case buf[pos-1] != '\n':
			preSep = []byte("\n\n")
		case pos < 2 || buf[pos-2] != '\n':
			preSep = []byte("\n")
		}
	}
	var postSep []byte
	if pos < len(buf) {
		// rep is normalized to end in '\n'. If the byte at pos is
		// non-blank-line content (e.g. the start of the next declared
		// heading), insert an extra '\n' so the boundary has a blank
		// line between them.
		endsDouble := bytes.HasSuffix(rep, []byte("\n\n"))
		if !endsDouble {
			postSep = []byte("\n")
		}
	}
	out := make([]byte, 0, len(buf)+len(preSep)+len(rep)+len(postSep))
	out = append(out, buf[:pos]...)
	out = append(out, preSep...)
	out = append(out, rep...)
	out = append(out, postSep...)
	out = append(out, buf[pos:]...)
	return out
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
// path does not contain '.'.
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
