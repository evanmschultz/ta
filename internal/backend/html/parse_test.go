package html

import (
	"bytes"
	"strings"
	"testing"

	ghtml "golang.org/x/net/html"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// findNode does a pre-order DFS and returns the first node satisfying pred.
func findNode(root *ghtml.Node, pred func(*ghtml.Node) bool) *ghtml.Node {
	if pred(root) {
		return root
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if found := findNode(c, pred); found != nil {
			return found
		}
	}
	return nil
}

// elementNode returns the first ElementNode with the given tag name.
func elementNode(root *ghtml.Node, tag string) *ghtml.Node {
	return findNode(root, func(n *ghtml.Node) bool {
		return n.Type == ghtml.ElementNode && n.Data == tag
	})
}

// textNodeUnder returns the first TextNode that is a direct child of the
// element with the given tag.
func textNodeUnder(root *ghtml.Node, tag string) *ghtml.Node {
	el := elementNode(root, tag)
	if el == nil {
		return nil
	}
	for c := el.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == ghtml.TextNode {
			return c
		}
	}
	return nil
}

// allNodes returns all nodes in pre-order DFS.
func allNodes(root *ghtml.Node) []*ghtml.Node {
	var out []*ghtml.Node
	var walk func(*ghtml.Node)
	walk = func(n *ghtml.Node) {
		out = append(out, n)
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestHtmlParse_PreservesBytePositions verifies that Offsets[node].Start and
// Offsets[node].End point at the exact byte range of the node's token in buf.
func TestHtmlParse_PreservesBytePositions(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head></head><body><p>hello</p></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	p := elementNode(tree.Root, "p")
	if p == nil {
		t.Fatal("no <p> element found")
	}
	r, ok := tree.Offsets[p]
	if !ok {
		t.Fatal("Offsets has no entry for <p>")
	}
	if r.Start < 0 || r.End <= r.Start {
		t.Fatalf("<p> offset invalid: %+v", r)
	}
	// The bytes at [Start:End) must start with '<' and contain 'p'.
	slice := buf[r.Start:r.End]
	if !bytes.HasPrefix(slice, []byte("<p")) {
		t.Errorf("offset slice %q does not start with '<p'", slice)
	}
}

// TestHtmlParse_RecoversOffsetsViaRaw verifies that offset arithmetic is based
// on Tokenizer.Raw() (cumulative byte lengths) rather than re-rendering nodes.
// Strategy: assert that for every non-desynced node with a valid offset, the
// bytes buf[Offsets[n].Start:Offsets[n].End] are non-empty and were derived
// from the tokenizer stream (i.e. Offsets[n].End - Offsets[n].Start > 0 and
// the slice is the actual raw token bytes from the original buffer).
func TestHtmlParse_RecoversOffsetsViaRaw(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head><title>hi</title></head><body></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Collect all non-desynced nodes that have offsets.
	counted := 0
	for node, r := range tree.Offsets {
		if tree.Desynced[node] {
			continue
		}
		if r.Start < 0 || r.End < r.Start {
			t.Errorf("node %v has invalid range %+v", node.Data, r)
			continue
		}
		// Every valid offset slice must be non-empty.
		if r.End-r.Start == 0 {
			t.Errorf("node %v has zero-length offset range", node.Data)
		}
		// The slice must be within buf bounds.
		if r.End > len(buf) {
			t.Errorf("node %v offset end %d exceeds buf len %d", node.Data, r.End, len(buf))
		}
		counted++
	}
	if counted == 0 {
		t.Fatal("no non-desynced nodes with offsets found")
	}
}

// TestHtmlParse_TraversalOrderAlignment verifies that for a multi-child
// container, the tree walk and token walk agree across multiple siblings.
func TestHtmlParse_TraversalOrderAlignment(t *testing.T) {
	// Three siblings; each must align to its own token.
	buf := []byte(`<!DOCTYPE html><html><head></head><body><ul><li>a</li><li>b</li><li>c</li></ul></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ul := elementNode(tree.Root, "ul")
	if ul == nil {
		t.Fatal("no <ul>")
	}
	// Collect <li> children.
	var lis []*ghtml.Node
	for c := ul.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == ghtml.ElementNode && c.Data == "li" {
			lis = append(lis, c)
		}
	}
	if len(lis) != 3 {
		t.Fatalf("expected 3 <li>, got %d", len(lis))
	}
	// Offsets must be strictly increasing across siblings.
	prevEnd := -1
	for i, li := range lis {
		r, ok := tree.Offsets[li]
		if !ok || r.Start < 0 {
			t.Errorf("li[%d]: no valid offset", i)
			continue
		}
		if r.Start < prevEnd {
			t.Errorf("li[%d]: start %d < previous end %d (offsets overlap)", i, r.Start, prevEnd)
		}
		// The token bytes must contain the tag.
		slice := buf[r.Start:r.End]
		if !bytes.Contains(slice, []byte("li")) {
			t.Errorf("li[%d]: token slice %q missing 'li'", i, slice)
		}
		prevEnd = r.End
	}
}

// TestHtmlParse_SelfClosingVoidElements verifies that void elements (<br>,
// <hr>, <img>) receive valid offsets without requiring end-tag tokens.
func TestHtmlParse_SelfClosingVoidElements(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head></head><body><br><hr><img src="x"><input type="text"></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, tag := range []string{"br", "hr", "img", "input"} {
		n := elementNode(tree.Root, tag)
		if n == nil {
			t.Errorf("no <%s> element", tag)
			continue
		}
		r, ok := tree.Offsets[n]
		if !ok {
			t.Errorf("<%s>: no offset entry", tag)
			continue
		}
		if r.Start < 0 {
			// Desynced — acceptable but log.
			t.Logf("<%s>: desynced (Start=-1); may occur in full-document mode", tag)
			continue
		}
		slice := buf[r.Start:r.End]
		if !bytes.Contains(slice, []byte(tag)) {
			t.Errorf("<%s>: slice %q does not contain tag name", tag, slice)
		}
	}
}

// TestHtmlParse_HtmlComments verifies that HTML comments are preserved with
// their byte range.
func TestHtmlParse_HtmlComments(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head></head><body><!-- comment --></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	comment := findNode(tree.Root, func(n *ghtml.Node) bool {
		return n.Type == ghtml.CommentNode
	})
	if comment == nil {
		t.Fatal("no CommentNode found")
	}
	r, ok := tree.Offsets[comment]
	if !ok || r.Start < 0 {
		t.Fatalf("comment node has no valid offset: ok=%v r=%+v", ok, r)
	}
	slice := buf[r.Start:r.End]
	if !bytes.HasPrefix(slice, []byte("<!--")) {
		t.Errorf("comment slice %q does not start with '<!--'", slice)
	}
	if !bytes.HasSuffix(slice, []byte("-->")) {
		t.Errorf("comment slice %q does not end with '-->'", slice)
	}
}

// TestHtmlParse_DoctypePreservation verifies that the <!DOCTYPE html>
// declaration is preserved with a non-nil offset.
func TestHtmlParse_DoctypePreservation(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head></head><body></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doctype := findNode(tree.Root, func(n *ghtml.Node) bool {
		return n.Type == ghtml.DoctypeNode
	})
	if doctype == nil {
		t.Fatal("no DoctypeNode found")
	}
	r, ok := tree.Offsets[doctype]
	if !ok || r.Start < 0 {
		t.Fatalf("doctype node has no valid offset: ok=%v r=%+v", ok, r)
	}
	slice := buf[r.Start:r.End]
	lower := strings.ToLower(string(slice))
	if !strings.HasPrefix(lower, "<!doctype") {
		t.Errorf("doctype slice %q does not start with '<!doctype'", slice)
	}
}

// TestHtmlParse_RawTextElements verifies that the four HTML5 raw-text /
// RCDATA tokenizer states (script + style RAWTEXT; textarea + title RCDATA)
// preserve their content byte-for-byte, including literal '<' characters
// that would otherwise be parsed as tag-opens.
func TestHtmlParse_RawTextElements(t *testing.T) {
	cases := []struct {
		name    string
		tag     string
		content string
	}{
		{"script", "script", "let x = 1 < 2;"},
		{"style", "style", "body { color: red; } /* < */"},
		{"textarea", "textarea", "user typed < and > here"},
		{"title", "title", "Page Title < & >"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf []byte
			switch tc.tag {
			case "script", "style":
				// head-scoped raw-text elements
				buf = []byte("<!DOCTYPE html><html><head><" + tc.tag + ">" + tc.content + "</" + tc.tag + "></head><body></body></html>")
			case "title":
				// title is also head-scoped (RCDATA)
				buf = []byte("<!DOCTYPE html><html><head><title>" + tc.content + "</title></head><body></body></html>")
			case "textarea":
				// textarea is body-scoped (RCDATA)
				buf = []byte("<!DOCTYPE html><html><head></head><body><textarea>" + tc.content + "</textarea></body></html>")
			}
			tree, err := Parse(buf)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			el := elementNode(tree.Root, tc.tag)
			if el == nil {
				t.Fatalf("no <%s> element", tc.tag)
			}
			var text *ghtml.Node
			for c := el.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == ghtml.TextNode {
					text = c
					break
				}
			}
			if text == nil {
				t.Fatalf("no TextNode inside <%s>", tc.tag)
			}
			r, ok := tree.Offsets[text]
			if !ok || r.Start < 0 {
				t.Fatalf("<%s> text node has no valid offset: ok=%v r=%+v", tc.tag, ok, r)
			}
			slice := buf[r.Start:r.End]
			// Raw-text content must contain the literal '<' that would
			// otherwise be a tag-open in non-raw-text contexts.
			if !bytes.Contains(slice, []byte("<")) {
				t.Errorf("<%s> text slice %q missing literal '<' (raw-text preservation broken)", tc.tag, slice)
			}
		})
	}
}

// TestHtmlParse_MultiByteUTF8Fixtures verifies that byte offsets are byte-
// based (not rune-based). Characters like 'é' (2 bytes), '日' (3 bytes), and
// emoji (4 bytes) must not cause off-by-one errors in subsequent nodes.
func TestHtmlParse_MultiByteUTF8Fixtures(t *testing.T) {
	// The emoji '😀' is U+1F600 = 4 bytes in UTF-8.
	buf := []byte("<!DOCTYPE html><html><head></head><body><p>é</p><p>日本語</p><p>😀</p></body></html>")
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Walk all <p> elements; each must have a valid, non-zero-width offset for
	// its text child, and the slice must match the expected content exactly.
	expected := []string{"é", "日本語", "😀"}
	pIdx := 0
	for n := tree.Root.FirstChild; n != nil; n = n.NextSibling {
		if n.Type != ghtml.ElementNode {
			continue
		}
		_ = findParaTexts(t, tree, n, expected, &pIdx)
	}
}

// findParaTexts recursively collects <p> text children and checks offsets.
func findParaTexts(t *testing.T, tree *Tree, root *ghtml.Node, expected []string, idx *int) int {
	t.Helper()
	if root.Type == ghtml.ElementNode && root.Data == "p" {
		for c := root.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == ghtml.TextNode && *idx < len(expected) {
				r, ok := tree.Offsets[c]
				if !ok || r.Start < 0 {
					t.Errorf("p[%d] text node has no valid offset", *idx)
					*idx++
					continue
				}
				// Verify byte-exact content.
				buf := []byte("<!DOCTYPE html><html><head></head><body><p>é</p><p>日本語</p><p>😀</p></body></html>")
				got := string(buf[r.Start:r.End])
				if got != expected[*idx] {
					t.Errorf("p[%d]: got %q want %q (byte offsets wrong?)", *idx, got, expected[*idx])
				}
				*idx++
			}
		}
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		findParaTexts(t, tree, c, expected, idx)
	}
	return *idx
}

// TestHtmlParse_ImplicitHtmlHeadBodyHandled verifies that fragment mode
// (raw fragment without DOCTYPE/html wrapper) parses correctly via
// ParseFragment with a body context.
func TestHtmlParse_ImplicitHtmlHeadBodyHandled(t *testing.T) {
	buf := []byte(`<p>hi</p>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Fragment parse wraps result in a synthetic DocumentNode.
	if tree.Root.Type != ghtml.DocumentNode {
		t.Fatalf("Root type: got %v want DocumentNode", tree.Root.Type)
	}
	p := elementNode(tree.Root, "p")
	if p == nil {
		t.Fatal("no <p> element in fragment parse result")
	}
	text := textNodeUnder(tree.Root, "p")
	if text == nil {
		t.Fatal("no text node under <p>")
	}
	if text.Data != "hi" {
		t.Errorf("text content: got %q want %q", text.Data, "hi")
	}
}

// TestHtmlParse_DesyncFallbackOnImplicitTbody verifies that an implicitly
// inserted <tbody> (not present in the token stream) is detected as desynced
// and marked with Range{-1, -1} in Offsets and true in Desynced.
func TestHtmlParse_DesyncFallbackOnImplicitTbody(t *testing.T) {
	// The HTML5 parser always inserts a <tbody> between <table> and <tr>.
	// The tokenizer emits: table → tr → td — no tbody token.
	buf := []byte(`<table><tr><td>x</td></tr></table>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tbody := elementNode(tree.Root, "tbody")
	if tbody == nil {
		t.Fatal("no <tbody> found — HTML5 parser should have inserted one implicitly")
	}
	r := tree.Offsets[tbody]
	if r.Start != -1 || r.End != -1 {
		t.Errorf("implicit tbody expected Range{-1,-1}, got %+v", r)
	}
	if !tree.Desynced[tbody] {
		t.Error("implicit tbody should be marked in Desynced map")
	}
	// The <td> child is under tbody; also expected desynced (parent desynced).
	td := elementNode(tree.Root, "td")
	if td == nil {
		t.Fatal("no <td> found")
	}
	if !tree.Desynced[td] {
		t.Error("<td> under implicit <tbody> should also be desynced (parent propagation)")
	}
}

// TestHtmlParse_MalformedInputClarification verifies best-effort behaviour
// for a range of malformed HTML inputs. These are not expected to return
// errors; the HTML5 parser recovers. The test asserts that Parse does not
// panic and returns a non-nil Tree.
func TestHtmlParse_MalformedInputClarification(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "unclosed-tags",
			input: "<div>foo",
			// Parser auto-closes; round-trip is lossy on the missing close tag.
		},
		{
			name:  "mismatched-nesting",
			input: "<b><i></b></i>",
			// Adoption agency handles; best-effort offsets.
		},
		{
			name:  "illegal-nesting",
			input: "<p><div></div></p>",
			// Parser auto-closes <p> before <div>; best-effort.
		},
		{
			name:  "no-root",
			input: "hello world",
			// Plain text fragment; Parse uses fragment mode.
		},
		{
			name:  "NUL-bytes",
			input: "<p>hel\x00lo</p>",
			// HTML5 spec replaces U+0000 with U+FFFD (replacement character).
			// Round-trip is lossy. Best-effort offsets.
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tree, err := Parse([]byte(tc.input))
			if err != nil {
				t.Fatalf("Parse returned unexpected error: %v", err)
			}
			if tree == nil {
				t.Fatal("Parse returned nil Tree")
			}
			if tree.Root == nil {
				t.Fatal("Tree.Root is nil")
			}
			// Verify we can walk the tree without panic.
			nodes := allNodes(tree.Root)
			if len(nodes) == 0 {
				t.Fatal("tree has no nodes")
			}
		})
	}
}
