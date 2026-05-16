// L3-D2-D3 wires the parse + splice layers of this package into the
// format.Format contract from internal/format. HtmlBackend is registered
// under the schema-enum key "html" at package init so format.Dispatch("html")
// resolves it without any further wiring.

package html

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/andybalholm/cascadia"
	"github.com/evanmschultz/ta/internal/format"
	ghtml "golang.org/x/net/html"
)

// HtmlBackend implements format.Format for HTML inputs. Parse / Find / Splice
// delegate to this package's own parse.go and splice.go; Marshal performs a
// best-effort concatenation of block bytes in source order.
//
// HtmlBackend is stateless — a single zero-value instance is registered with
// format.Register at package init.
type HtmlBackend struct{}

// compile-time guarantee: HtmlBackend satisfies format.Format. Mirrors the
// pattern used by stdlib backends and the format package's own test mocks.
var _ format.Format = (*HtmlBackend)(nil)

func init() {
	format.Register("html", &HtmlBackend{})
}

// nameToSelectorer is the minimal manifest surface HtmlBackend needs beyond
// format.Manifest itself: a name→selector lookup for the user-facing block
// names declared in the schema. Any caller-supplied manifest impl can satisfy
// this interface directly; *format.HtmlManifest is handled separately via
// htmlManifestAdapter (it does NOT implement NameToSelectorString on its
// own — the adapter bridges its NameToSelector map field to this interface).
// If a caller passes a Manifest that is neither type, Find / Splice return
// ErrBlockNotFound because no name can be resolved.
type nameToSelectorer interface {
	// NameToSelectorString reports the CSS selector declared for the
	// manifest block named name, or ("", false) when name is unknown.
	NameToSelectorString(name string) (string, bool)
}

// htmlManifestAdapter wraps *format.HtmlManifest's NameToSelector field as
// the nameToSelectorer interface above. Keeps backend.go independent of the
// concrete manifest struct shape — the backend looks for either the
// interface OR the known concrete type.
type htmlManifestAdapter struct {
	m *format.HtmlManifest
}

func (a htmlManifestAdapter) NameToSelectorString(name string) (string, bool) {
	if a.m == nil || a.m.NameToSelector == nil {
		return "", false
	}
	sel, ok := a.m.NameToSelector[name]
	return sel, ok
}

// resolveSelector pulls a CSS selector for the given manifest block name out
// of the caller-supplied Manifest. Recognises the concrete HtmlManifest type
// and the nameToSelectorer interface; returns ("", false) for anything else.
func resolveSelector(m format.Manifest, name string) (string, bool) {
	switch v := m.(type) {
	case nameToSelectorer:
		return v.NameToSelectorString(name)
	case *format.HtmlManifest:
		return htmlManifestAdapter{m: v}.NameToSelectorString(name)
	}
	return "", false
}

// Parse extracts every manifest-named block from buf. For each selector
// declared by m.Selectors(), cascadia matches against the parsed tree and
// every matched node contributes a Block whose Bytes span covers the node's
// full outer byte range (start tag through end tag). Blocks are ordered by
// their Start byte.
//
// Unknown / unmatched selectors silently contribute zero blocks — Parse is
// not the validation surface; the manifest loader and Find are.
func (b *HtmlBackend) Parse(buf []byte, m format.Manifest) (format.Blocks, error) {
	tree, err := Parse(buf)
	if err != nil {
		return nil, fmt.Errorf("html backend: parse: %w", err)
	}
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
	seen := make(map[*ghtml.Node]string) // node → block name; when multiple selectors overlap on the same node, the FIRST selector iterated wins. format.HtmlManifest.Selectors() sorts by block-name alphabetic order (see manifest.go normalizeSelectors), so overlapping nodes get labeled by the alphabetically-earliest block name. Deterministic but ordering is name-driven, not selector-precedence-driven.

	for _, sel := range m.Selectors() {
		compiled, cerr := cascadia.Compile(sel)
		if cerr != nil {
			return nil, fmt.Errorf("html backend: parse: compile selector %q: %w", sel, cerr)
		}
		name, ok := m.BlockName(sel)
		if !ok {
			// Selector advertised by manifest but BlockName won't resolve it —
			// surfaces a manifest internal inconsistency; skip silently and
			// let the manifest loader catch this elsewhere.
			continue
		}
		for _, node := range cascadia.QueryAll(tree.Root, compiled) {
			if _, dup := seen[node]; dup {
				continue
			}
			r, ok := outerRange(tree, buf, node)
			if !ok || r.Start < 0 || r.End < r.Start || r.End > len(buf) {
				continue
			}
			seen[node] = name
			hits = append(hits, emitted{
				name:  name,
				start: r.Start,
				end:   r.End,
				bytes: append([]byte(nil), buf[r.Start:r.End]...),
			})
		}
	}

	// Sort by Start byte for source-order traversal. Insertion-sort is fine
	// for the manifest-bounded N we expect; avoids pulling sort just for one
	// call.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j-1].start > hits[j].start; j-- {
			hits[j-1], hits[j] = hits[j], hits[j-1]
		}
	}

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

// Find returns the byte range of the named block in buf, resolved via the
// manifest's name→selector mapping. Returns format.ErrBlockNotFound when
// name is unknown to the manifest or when the selector matches zero nodes.
// Ambiguous matches return the first match's bytes plus a wrapped
// ErrAmbiguousMatch so callers can errors.Is-detect the ambiguity.
func (b *HtmlBackend) Find(buf []byte, m format.Manifest, name string) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("html backend: find: %w", format.ErrBlockNotFound)
	}
	selector, ok := resolveSelector(m, name)
	if !ok {
		return nil, fmt.Errorf("html backend: find: name %q: %w", name, format.ErrBlockNotFound)
	}

	tree, err := Parse(buf)
	if err != nil {
		return nil, fmt.Errorf("html backend: find: parse: %w", err)
	}

	compiled, cerr := cascadia.Compile(selector)
	if cerr != nil {
		return nil, fmt.Errorf("html backend: find: compile selector %q: %w", selector, cerr)
	}

	matches := cascadia.QueryAll(tree.Root, compiled)
	if len(matches) == 0 {
		return nil, fmt.Errorf("html backend: find: selector %q for name %q matched zero nodes: %w", selector, name, format.ErrBlockNotFound)
	}

	r, ok := outerRange(tree, buf, matches[0])
	if !ok || r.Start < 0 || r.End < r.Start || r.End > len(buf) {
		return nil, fmt.Errorf("html backend: find: matched node has invalid offset range")
	}
	result := append([]byte(nil), buf[r.Start:r.End]...)

	if len(matches) > 1 {
		return result, fmt.Errorf("html backend: find: %d nodes matched selector %q for name %q: %w", len(matches), selector, name, ErrAmbiguousMatch)
	}
	return result, nil
}

// Splice replaces the named block's outer byte range in buf with content.
// Resolves name→selector via the manifest, then delegates to the package's
// Splice helper. format.ErrBlockNotFound is returned when name is unknown
// OR when the resolved selector matches zero nodes; in the latter case the
// underlying html.ErrSelectorNotFound is INTENTIONALLY TRANSLATED to
// format.ErrBlockNotFound (the unified not-found sentinel at the format
// layer) — `errors.Is(err, html.ErrSelectorNotFound)` will return false
// post-translation. This is by design per the L3-D1 substrate's unification
// of three not-found sources into format.ErrBlockNotFound. Multi-match
// returns the first-match splice plus a %w-wrapped ErrAmbiguousMatch.
func (b *HtmlBackend) Splice(buf []byte, m format.Manifest, name string, content []byte) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("html backend: splice: %w", format.ErrBlockNotFound)
	}
	selector, ok := resolveSelector(m, name)
	if !ok {
		return nil, fmt.Errorf("html backend: splice: name %q: %w", name, format.ErrBlockNotFound)
	}

	tree, err := Parse(buf)
	if err != nil {
		return nil, fmt.Errorf("html backend: splice: parse: %w", err)
	}

	out, err := Splice(tree, buf, selector, content)
	if err != nil {
		if errors.Is(err, ErrSelectorNotFound) {
			return nil, fmt.Errorf("html backend: splice: name %q (selector %q) matched zero nodes: %w", name, selector, format.ErrBlockNotFound)
		}
		// ErrAmbiguousMatch returns a non-nil buffer alongside its sentinel;
		// propagate both so callers can errors.Is-detect ambiguity while
		// consuming the first-match-wins result.
		return out, fmt.Errorf("html backend: splice: name %q: %w", name, err)
	}
	return out, nil
}

// Marshal concatenates each block's Bytes in source order, separated by a
// single newline. This is a round-trip helper for output produced by Parse —
// it is NOT a general HTML emitter. For arbitrary HTML synthesis use a
// template engine.
//
// Round-trip identity holds only when the parsed input consists entirely of
// manifest-named blocks with no surrounding text / whitespace; otherwise
// Marshal output omits the non-named regions and the round-trip is lossy.
func (b *HtmlBackend) Marshal(blocks format.Blocks, _ format.Manifest) ([]byte, error) {
	if len(blocks) == 0 {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	for i, blk := range blocks {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.Write(blk.Bytes)
	}
	return buf.Bytes(), nil
}
