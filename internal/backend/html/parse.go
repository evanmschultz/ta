// Package html implements an HTML parse layer for the backend pipeline.
//
// # Architecture
//
// Parse provides a dual-pass strategy to produce both a parsed *html.Node
// tree (via golang.org/x/net/html) and a side-table mapping each node to
// its byte range in the original buffer.
//
//   - Pass 1 (tree): html.Parse / html.ParseFragment produces the DOM tree.
//   - Pass 2 (offsets): html.NewTokenizer walks the original buffer in lock-step
//     with a pre-order DFS of the tree. Token offsets are derived from the
//     cumulative sum of Raw() lengths — the raw bytes partition the stream
//     with no gaps or overlaps.
//
// # Desync detection
//
// The HTML5 parser performs tree surgery that the tokenizer does not: implicit
// element insertion (e.g. tbody inside table, html/head/body wrappers) and
// adoption-agency restructuring. When a tree-walk step and the next token
// step disagree on tag name at position N, the alignment is declared desynced
// for that subtree. Desynced nodes receive Range{Start: -1, End: -1} in the
// Offsets map. The L3-D2-D2 splice layer consults Offsets and falls back to
// a signature-based re-tokenization scan for desynced nodes.
//
// # Forward dependency
//
// The blank import of "github.com/andybalholm/cascadia" below stages the
// direct-dependency declaration needed by the L3-D2-D3 backend layer (CSS
// selector querying). It is intentionally unused here; the blank import
// prevents go mod tidy from stripping it before D3 lands.
package html

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	// cascadia is used in L3-D2-D3 (CSS selector queries on the parsed tree).
	// Blank-imported here so the direct-dep declaration survives go mod tidy
	// until that slice lands.
	_ "github.com/andybalholm/cascadia"
	ghtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Range is a half-open byte interval [Start, End) into the original parse
// buffer. A Range with Start == -1 signals a desynced node whose byte
// position could not be determined by alignment (see package-level desync
// detection note).
type Range struct {
	Start int
	End   int
}

// Tree pairs a parsed *ghtml.Node tree with a side-table mapping nodes to
// byte ranges in the original buffer.
//
// Root is always a DocumentNode. For fragment input, the document node is
// synthetic; its children are the parsed fragment nodes. The synthetic
// DocumentNode itself has NO entry in Offsets — only real parsed nodes are
// indexed. Consumers that need the full input-buffer range should use
// [0, len(buf)) directly rather than looking up the synthetic root.
//
// Desynced contains nodes where tree-order and token-order diverge (e.g.
// implicit tbody insertion). The splice layer uses this map to choose
// fallback offset recovery.
type Tree struct {
	Root     *ghtml.Node
	Offsets  map[*ghtml.Node]Range
	Desynced map[*ghtml.Node]bool
}

// voidElements is the complete HTML5 set of void elements — elements that
// must not have end tags and that the parser never emits EndTagTokens for.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// isFragment reports whether buf looks like an HTML fragment rather than a
// full document. A full document starts (after optional leading whitespace)
// with <!DOCTYPE or <html.
func isFragment(buf []byte) bool {
	trimmed := bytes.TrimSpace(buf)
	lower := strings.ToLower(string(trimmed))
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") {
		return false
	}
	return true
}

// Parse parses an HTML buffer and returns a Tree with offset metadata.
//
// Auto-detects fragment vs document by inspecting the leading bytes after
// whitespace trimming. Fragments are parsed with a body element as context
// (html.ParseFragment with a non-nil *html.Node{DataAtom: atom.Body}).
//
// On desync (implicit element insertion by the HTML5 parser), affected nodes
// are marked with Range{-1,-1} in Offsets and true in Desynced.
func Parse(buf []byte) (*Tree, error) {
	var (
		root *ghtml.Node
		err  error
	)

	if isFragment(buf) {
		ctx := &ghtml.Node{
			Type:     ghtml.ElementNode,
			DataAtom: atom.Body,
			Data:     "body",
		}
		nodes, ferr := ghtml.ParseFragment(bytes.NewReader(buf), ctx)
		if ferr != nil {
			return nil, fmt.Errorf("html parse fragment: %w", ferr)
		}
		// Wrap fragment nodes under a synthetic DocumentNode so callers
		// always receive a single-root tree.
		root = &ghtml.Node{Type: ghtml.DocumentNode}
		for _, n := range nodes {
			root.AppendChild(n)
		}
	} else {
		root, err = ghtml.Parse(bytes.NewReader(buf))
		if err != nil {
			return nil, fmt.Errorf("html parse: %w", err)
		}
	}

	offsets, desynced := buildOffsets(buf, root)
	return &Tree{
		Root:     root,
		Offsets:  offsets,
		Desynced: desynced,
	}, nil
}

// tokenEntry records one token's data alongside its byte range in the
// original buffer, for alignment against tree nodes.
type tokenEntry struct {
	tokenType ghtml.TokenType
	data      string // tag name (lower) for start/end/self-closing; raw data otherwise
	start     int
	end       int
}

// buildOffsets performs the dual-pass alignment:
//  1. Tokenize buf, recording each token's [start,end) byte range.
//  2. Walk the tree in pre-order DFS, aligning element/text/comment/doctype
//     nodes to the token sequence.
//
// When alignment detects a desync (tag name mismatch or token count
// mismatch), the affected node and all its descendants are marked desynced.
func buildOffsets(buf []byte, root *ghtml.Node) (map[*ghtml.Node]Range, map[*ghtml.Node]bool) {
	tokens := tokenize(buf)

	offsets := make(map[*ghtml.Node]Range)
	desynced := make(map[*ghtml.Node]bool)

	pos := 0 // index into tokens slice
	alignNode(root, tokens, &pos, offsets, desynced, false)
	return offsets, desynced
}

// tokenize runs the tokenizer over buf and returns the ordered token list
// with pre-computed [start, end) byte ranges.
//
// Byte positions come from the invariant documented in Tokenizer.Raw():
//
//	"The token stream's raw bytes partition the byte stream (until ErrorToken).
//	 There are no overlaps or gaps between two consecutive token's raw bytes.
//	 The byte offset of the current token is the sum of the lengths of all
//	 previous tokens' raw bytes."
func tokenize(buf []byte) []tokenEntry {
	z := ghtml.NewTokenizer(bytes.NewReader(buf))
	var out []tokenEntry
	cursor := 0
	for {
		tt := z.Next()
		raw := z.Raw()
		start := cursor
		end := cursor + len(raw)
		cursor = end

		switch tt {
		case ghtml.ErrorToken:
			// End of input (or genuine parse error). Stop.
			return out
		case ghtml.StartTagToken, ghtml.SelfClosingTagToken:
			name, _ := z.TagName()
			out = append(out, tokenEntry{
				tokenType: tt,
				data:      string(name),
				start:     start,
				end:       end,
			})
		case ghtml.EndTagToken:
			name, _ := z.TagName()
			out = append(out, tokenEntry{
				tokenType: tt,
				data:      string(name),
				start:     start,
				end:       end,
			})
		case ghtml.TextToken, ghtml.CommentToken, ghtml.DoctypeToken:
			out = append(out, tokenEntry{
				tokenType: tt,
				data:      "",
				start:     start,
				end:       end,
			})
		}
	}
}

// alignNode assigns the byte range of the matching token to node, then
// recurses into children. parentDesynced propagates desync down the subtree.
func alignNode(
	node *ghtml.Node,
	tokens []tokenEntry,
	pos *int,
	offsets map[*ghtml.Node]Range,
	desynced map[*ghtml.Node]bool,
	parentDesynced bool,
) {
	switch node.Type {
	case ghtml.DocumentNode:
		// Document node itself has no token; recurse into children.
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			alignNode(c, tokens, pos, offsets, desynced, parentDesynced)
		}
		return

	case ghtml.ElementNode:
		// Consume the matching StartTagToken or SelfClosingTagToken.
		if parentDesynced || !consumeStartTag(node, tokens, pos, offsets, desynced) {
			// Desync: mark this node and propagate.
			desynced[node] = true
			offsets[node] = Range{Start: -1, End: -1}
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				alignNode(c, tokens, pos, offsets, desynced, true)
			}
			return
		}
		// Recurse into children.
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			alignNode(c, tokens, pos, offsets, desynced, false)
		}
		// Consume the matching EndTagToken (absent for void elements).
		if !voidElements[node.Data] {
			consumeEndTag(node, tokens, pos)
		}

	case ghtml.TextNode:
		if parentDesynced || *pos >= len(tokens) || tokens[*pos].tokenType != ghtml.TextToken {
			desynced[node] = true
			offsets[node] = Range{Start: -1, End: -1}
			return
		}
		offsets[node] = Range{Start: tokens[*pos].start, End: tokens[*pos].end}
		*pos++

	case ghtml.CommentNode:
		if parentDesynced || *pos >= len(tokens) || tokens[*pos].tokenType != ghtml.CommentToken {
			desynced[node] = true
			offsets[node] = Range{Start: -1, End: -1}
			return
		}
		offsets[node] = Range{Start: tokens[*pos].start, End: tokens[*pos].end}
		*pos++

	case ghtml.DoctypeNode:
		if parentDesynced || *pos >= len(tokens) || tokens[*pos].tokenType != ghtml.DoctypeToken {
			desynced[node] = true
			offsets[node] = Range{Start: -1, End: -1}
			return
		}
		offsets[node] = Range{Start: tokens[*pos].start, End: tokens[*pos].end}
		*pos++
	}
}

// consumeStartTag attempts to match node against the start/self-closing token
// at tokens[*pos]. On success, records the offset and advances pos. Returns
// false (desync) if token type or tag name does not match.
func consumeStartTag(
	node *ghtml.Node,
	tokens []tokenEntry,
	pos *int,
	offsets map[*ghtml.Node]Range,
	desynced map[*ghtml.Node]bool,
) bool {
	if *pos >= len(tokens) {
		return false
	}
	te := tokens[*pos]
	if te.tokenType != ghtml.StartTagToken && te.tokenType != ghtml.SelfClosingTagToken {
		return false
	}
	if te.data != node.Data {
		return false
	}
	offsets[node] = Range{Start: te.start, End: te.end}
	*pos++
	return true
}

// consumeEndTag advances past the matching EndTagToken, if present.
// End tags may be absent for optional-close elements (e.g. <li>, <p>).
// If no matching end tag is at pos, it silently returns — the tree is
// still valid, just lossy on the closing-tag byte range.
func consumeEndTag(node *ghtml.Node, tokens []tokenEntry, pos *int) {
	if *pos >= len(tokens) {
		return
	}
	te := tokens[*pos]
	if te.tokenType == ghtml.EndTagToken && te.data == node.Data {
		*pos++
	}
}

// Ensure io is used (it is; bytes.NewReader satisfies io.Reader). This
// compile-time assertion documents the io import intent.
var _ io.Reader = (*bytes.Reader)(nil)
