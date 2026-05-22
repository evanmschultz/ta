package html

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/andybalholm/cascadia"
	"github.com/evanmschultz/ta/internal/format"
	ghtml "golang.org/x/net/html"
)

// ErrSelectorNotFound is returned by Splice when the supplied CSS selector
// compiles successfully but matches zero nodes in the parsed tree.
//
// This is the surgical-fold #3 contract from L3-D2-D1's parse.go: a
// zero-match condition is distinct from a malformed selector (which yields
// a wrapped compile error) and from an ambiguous-match condition (which
// yields ErrAmbiguousMatch alongside a best-effort spliced buffer).
var ErrSelectorNotFound = errors.New("html: selector did not match")

// Splice returns a new buffer with the byte range of the first node matched
// by selector replaced by content.
//
// Contract:
//   - tree must be a *Tree produced by Parse(buf) on the same buf. Splice
//     does not re-parse — it looks up byte offsets from tree.Offsets.
//   - selector is a CSS selector compiled by cascadia.
//   - On zero matches, returns (nil, ErrSelectorNotFound).
//   - On a single match, returns (newBuf, nil) where newBuf preserves every
//     byte of buf outside the matched node's [Start, End) byte range.
//   - On multiple matches, returns (newBuf, wrapErr) where newBuf uses the
//     first match's byte range AND wrapErr satisfies
//     errors.Is(wrapErr, ErrAmbiguousMatch). This is the "first-match-wins
//     with ambiguous signal" contract.
//   - On a desynced node (Offsets entry is Range{-1,-1}), returns a clear
//     "not yet supported" error. Hardening signature-based re-tokenization
//     is deferred to a follow-up slice.
//
// Splice never mutates buf. The returned slice is a freshly-allocated copy.
func Splice(tree *Tree, buf []byte, selector string, content []byte) ([]byte, error) {
	if tree == nil {
		return nil, fmt.Errorf("nil tree")
	}
	if tree.Root == nil {
		return nil, fmt.Errorf("tree has nil root")
	}

	sel, err := cascadia.Compile(selector)
	if err != nil {
		return nil, fmt.Errorf("compile selector %q: %w", selector, err)
	}

	matches := cascadia.QueryAll(tree.Root, sel)
	if len(matches) == 0 {
		return nil, ErrSelectorNotFound
	}

	node := matches[0]
	r, ok := outerRange(tree, buf, node)
	if !ok {
		return nil, fmt.Errorf("matched node has no offset entry (synthetic or post-parse node?)")
	}
	if tree.Desynced[node] || r.Start < 0 || r.End < r.Start {
		return nil, fmt.Errorf("matched node is desynced; signature-based recovery not yet supported, re-parse fragment-mode")
	}
	if r.End > len(buf) {
		return nil, fmt.Errorf("node offset end %d exceeds buf length %d", r.End, len(buf))
	}

	out := make([]byte, 0, len(buf)-(r.End-r.Start)+len(content))
	out = append(out, buf[:r.Start]...)
	out = append(out, content...)
	out = append(out, buf[r.End:]...)

	if len(matches) > 1 {
		return out, fmt.Errorf("%d nodes matched selector %q: %w", len(matches), selector, format.ErrAmbiguousMatch)
	}
	return out, nil
}

// outerRange computes the full outer-byte span of node in buf, covering the
// start tag, all children, and the end tag (if present).
//
// parse.go's Tree.Offsets stores per-token byte ranges (e.g. an ElementNode's
// Offsets entry covers ONLY its start tag, not its descendants or end tag).
// Splice must operate on the element's full outer span so that the new
// content replaces the entire <tag>...</tag> region rather than just the
// opening tag.
//
// Algorithm:
//   - Non-element nodes (Text / Comment / Doctype): the offset entry already
//     covers the full byte range — return it as-is.
//   - Element nodes:
//   - start = Offsets[node].Start
//   - If void (no end tag in HTML5): end = Offsets[node].End
//   - Else: walk descendants and take the maximum End across all of them
//     (or fall back to Offsets[node].End for empty elements), then scan
//     forward in buf for the literal "</tag>" close-tag and include it.
//
// Returns (Range{-1,-1}, false) when the node has no offset entry or its
// recorded range is invalid.
func outerRange(tree *Tree, buf []byte, node *ghtml.Node) (Range, bool) {
	r, ok := tree.Offsets[node]
	if !ok {
		return Range{-1, -1}, false
	}
	if r.Start < 0 || r.End < r.Start {
		return r, true // propagate invalid range upward; caller handles desync.
	}

	if node.Type != ghtml.ElementNode {
		return r, true
	}

	// Void elements have no end tag — the start tag IS the full outer span.
	if voidElements[node.Data] {
		return r, true
	}

	// Walk descendants, tracking the maximum End we've seen. Skip desynced
	// descendants (they have Range{-1,-1}).
	maxEnd := r.End
	var walk func(*ghtml.Node)
	walk = func(n *ghtml.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if cr, ok := tree.Offsets[c]; ok && cr.Start >= 0 && cr.End >= cr.Start {
				if cr.End > maxEnd {
					maxEnd = cr.End
				}
			}
			walk(c)
		}
	}
	walk(node)

	// Scan forward from maxEnd looking for the literal "</tag>" close tag.
	// This is byte-level scanning intentionally — re-tokenization is more
	// expensive and the close-tag literal is unambiguous in well-formed
	// regions (raw-text / RCDATA elements that contain literal '</' inside
	// their text content have those bytes inside the TextNode's own offset
	// range, so maxEnd already sits past them).
	closeTag := []byte("</" + node.Data)
	if idx := bytes.Index(buf[maxEnd:], closeTag); idx >= 0 {
		// Find the trailing '>' of the close tag (may have whitespace).
		searchFrom := maxEnd + idx + len(closeTag)
		if gt := bytes.IndexByte(buf[searchFrom:], '>'); gt >= 0 {
			return Range{Start: r.Start, End: searchFrom + gt + 1}, true
		}
	}
	// No close tag found (lossy parser recovery for malformed input).
	// Best-effort: use maxEnd as the right boundary.
	return Range{Start: r.Start, End: maxEnd}, true
}
