package md_explicit

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/evanmschultz/ta/internal/format"
)

// Backend is the format.Format implementation for the md_explicit
// substrate — markdown blocks addressed by explicit heading-path
// selectors. It is registered under the schema-enum key "md" (NOT
// "md_explicit") because the format substrate's notion of "md" is the
// per-format backend that handles markdown buffers via the heading-path
// manifest, and only one Format may own that key (per drop_004 L3-D3
// amendment 2).
//
// Backend is stateless; a single zero-value instance is registered at
// package init.
//
// Coexistence with internal/backend/md/: the md/ package implements
// record.Backend (a DIFFERENT interface — schema-driven section store
// for the on-disk record tree). It does NOT call format.Register, so
// the "md" key in the format registry is exclusively owned by this
// package. The two backends address disjoint slices of the system:
// record.Backend for record CRUD against .ta/-rooted record files,
// format.Format for buffer-level Parse/Find/Splice/Marshal against
// caller-supplied bytes + a manifest.
type Backend struct{}

// compile-time assertion: *Backend satisfies format.Format. Mirrors the
// pattern used by html and other backends.
var _ format.Format = (*Backend)(nil)

func init() {
	format.Register("md", &Backend{})
}

// nameToSelectorer is the minimal manifest surface this backend needs
// beyond format.Manifest itself: a name → heading-path-selector lookup.
// Any caller-supplied manifest impl can satisfy this directly; the
// concrete *format.MdManifest is handled via mdManifestAdapter below.
type nameToSelectorer interface {
	// NameToSelectorString reports the heading-path selector declared
	// for the manifest block named name, or ("", false) when name is
	// unknown.
	NameToSelectorString(name string) (string, bool)
}

// mdManifestAdapter wraps *format.MdManifest's NameToSelector field as
// nameToSelectorer. Keeps backend.go independent of the concrete
// manifest struct shape — the backend looks for either the interface
// OR the known concrete type.
type mdManifestAdapter struct {
	m *format.MdManifest
}

func (a mdManifestAdapter) NameToSelectorString(name string) (string, bool) {
	if a.m == nil || a.m.NameToSelector == nil {
		return "", false
	}
	sel, ok := a.m.NameToSelector[name]
	return sel, ok
}

// resolveSelector pulls a heading-path selector for the manifest block
// name out of the caller-supplied Manifest. Recognises both the
// concrete *format.MdManifest and any impl satisfying nameToSelectorer;
// returns ("", false) for anything else.
func resolveSelector(m format.Manifest, name string) (string, bool) {
	switch v := m.(type) {
	case nameToSelectorer:
		return v.NameToSelectorString(name)
	case *format.MdManifest:
		return mdManifestAdapter{m: v}.NameToSelectorString(name)
	}
	return "", false
}

// Parse extracts every manifest-declared block from buf. For each
// selector in m.Selectors(), FindByPath resolves the heading-path
// against the buffer; matched heading nodes contribute a Block whose
// Bytes span covers the heading line through the start of the next
// sibling-or-shallower heading (or EOF). Blocks are sorted by Start
// for source-order traversal.
//
// Unknown / unmatched selectors silently contribute zero blocks —
// Parse is not the validation surface; the manifest loader and Find
// are. A nil manifest yields an empty Blocks slice.
//
// Multi-match note: when the SAME selector path matches multiple
// heading subtrees (e.g. duplicate heading-text under different roots),
// Parse currently emits ONLY the first match because FindByPath returns
// the first hit. This is consistent with the duplicate-path policy
// implemented in Find/Splice but does mean Parse output is incomplete
// in the rare duplicate-heading-path case. Find/Splice surface
// ErrAmbiguousMatch so callers can detect and tighten the selector.
func (b *Backend) Parse(buf []byte, m format.Manifest) (format.Blocks, error) {
	if m == nil {
		return format.Blocks{}, nil
	}
	type emitted struct {
		name  string
		start int
		end   int
		bytes []byte
	}
	var hits []emitted
	for _, sel := range m.Selectors() {
		name, ok := m.BlockName(sel)
		if !ok {
			// Selector advertised by manifest but BlockName won't
			// resolve it — surfaces a manifest internal inconsistency;
			// skip silently and let the manifest loader catch this.
			continue
		}
		node, body, err := FindByPath(buf, sel)
		if err != nil {
			// ErrBlockNotFound is the expected miss; any other error
			// would be a substrate bug. Skip the miss.
			continue
		}
		hits = append(hits, emitted{
			name:  name,
			start: node.ByteRange[0],
			end:   node.ByteRange[1],
			bytes: append([]byte(nil), body...),
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].start < hits[j].start
	})
	out := make(format.Blocks, 0, len(hits))
	for _, h := range hits {
		out = append(out, format.Block{
			Name:  h.name,
			Bytes: h.bytes,
			Start: h.start,
			End:   h.end,
		})
	}
	return out, nil
}

// Find returns the body bytes of the named block in buf, resolved via
// the manifest's name → heading-path-selector mapping.
//
// Returns format.ErrBlockNotFound when name is unknown to the manifest
// or when the selector matches zero headings.
//
// Multi-match policy (drop_004 L3-D3 routed concern 2 — symmetry with
// html backend, NOT txt): when the selector matches more than one
// heading subtree, Find returns the FIRST-match bytes PLUS a wrapped
// format.ErrAmbiguousMatch sentinel. Callers use errors.Is to detect
// the ambiguity and either tighten the selector or consume the
// first-match result. (txt's Find returns nil + ErrAmbiguousMatch
// instead — md_explicit follows html's "first-match + wrapped sentinel"
// because the heading-path selector contract makes the first match
// stable and useful even under ambiguity.)
func (b *Backend) Find(buf []byte, m format.Manifest, name string) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("md_explicit backend: find: %w", format.ErrBlockNotFound)
	}
	selector, ok := resolveSelector(m, name)
	if !ok {
		return nil, fmt.Errorf("md_explicit backend: find: name %q: %w", name, format.ErrBlockNotFound)
	}
	matches := findAllByPath(buf, selector)
	if len(matches) == 0 {
		return nil, fmt.Errorf("md_explicit backend: find: selector %q for name %q matched zero headings: %w", selector, name, format.ErrBlockNotFound)
	}
	first := matches[0]
	body := append([]byte(nil), buf[first.ByteRange[0]:first.ByteRange[1]]...)
	if len(matches) > 1 {
		return body, fmt.Errorf("md_explicit backend: find: %d headings matched selector %q for name %q: %w", len(matches), selector, name, format.ErrAmbiguousMatch)
	}
	return body, nil
}

// Splice replaces the named block's byte range in buf with content,
// preserving every byte outside the touched range verbatim.
//
// Resolves name → selector via the manifest, then locates the heading
// node and replaces buf[node.ByteRange[0]:node.ByteRange[1]] with
// content. Returns format.ErrBlockNotFound when name is unknown OR
// when the selector matches zero headings.
//
// Multi-match (routed concern 2): same policy as Find — first-match
// splice, wrapped format.ErrAmbiguousMatch returned alongside the
// non-nil output buffer so callers can errors.Is-detect and decide
// whether to accept the first-match write or tighten the selector.
func (b *Backend) Splice(buf []byte, m format.Manifest, name string, content []byte) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("md_explicit backend: splice: %w", format.ErrBlockNotFound)
	}
	selector, ok := resolveSelector(m, name)
	if !ok {
		return nil, fmt.Errorf("md_explicit backend: splice: name %q: %w", name, format.ErrBlockNotFound)
	}
	matches := findAllByPath(buf, selector)
	if len(matches) == 0 {
		return nil, fmt.Errorf("md_explicit backend: splice: selector %q for name %q matched zero headings: %w", selector, name, format.ErrBlockNotFound)
	}
	first := matches[0]
	out := make([]byte, 0, len(buf)-(first.ByteRange[1]-first.ByteRange[0])+len(content))
	out = append(out, buf[:first.ByteRange[0]]...)
	out = append(out, content...)
	out = append(out, buf[first.ByteRange[1]:]...)
	if len(matches) > 1 {
		return out, fmt.Errorf("md_explicit backend: splice: %d headings matched selector %q for name %q: %w", len(matches), selector, name, format.ErrAmbiguousMatch)
	}
	return out, nil
}

// Marshal concatenates each block's Bytes in slice order with NO
// separator. Round-trip identity (Parse → Marshal == buf) holds only
// when the parsed input consists ENTIRELY of contiguous manifest-named
// blocks tiling the buffer end-to-end — typically a file whose root
// headings are all declared and whose body fully resides under those
// headings. For arbitrary markdown synthesis (or buffers with
// non-named preamble / trailing content), Marshal output is lossy.
//
// The no-separator policy is deliberate: FindByPath body spans already
// include the trailing newline of their span (each span runs from the
// heading's line start to the start of the NEXT same-or-shallower
// heading, which itself begins at a line boundary), so adjacent
// sibling blocks tile byte-identically.
func (b *Backend) Marshal(blocks format.Blocks, _ format.Manifest) ([]byte, error) {
	if len(blocks) == 0 {
		return []byte{}, nil
	}
	var out bytes.Buffer
	for _, blk := range blocks {
		out.Write(blk.Bytes)
	}
	return out.Bytes(), nil
}

// findAllByPath returns every heading node in buf that matches
// selector under the depth-adjacent match rules from FindByPath.
// Returns nil when no match. Used by Find/Splice to detect ambiguity
// without modifying scan.go's single-match contract.
func findAllByPath(buf []byte, selector string) []HeadingNode {
	segments := ParseSelector(selector)
	if len(segments) == 0 {
		return nil
	}
	headings := WalkHeadings(buf)
	if len(headings) == 0 {
		return nil
	}
	var out []HeadingNode
	for i, h := range headings {
		if h.Text != segments[0] {
			continue
		}
		matched, idx := matchRemaining(headings, i, segments)
		if matched {
			out = append(out, headings[idx])
		}
	}
	return out
}
