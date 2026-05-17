package md_explicit

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/format"
)

// TestMdExplicitScan_BuildsHeadingPath verifies a basic two-segment
// path resolves: `# A` then `## B` produces a heading tree where
// selector "A > B" returns the depth-2 node and its body bytes.
func TestMdExplicitScan_BuildsHeadingPath(t *testing.T) {
	buf := []byte("# A\nintro\n## B\nbody-b\n")

	nodes := WalkHeadings(buf)
	if len(nodes) != 2 {
		t.Fatalf("WalkHeadings: want 2 nodes, got %d (%+v)", len(nodes), nodes)
	}
	if nodes[0].Depth != 1 || nodes[0].Text != "A" {
		t.Fatalf("node 0: want {Depth:1 Text:A}, got %+v", nodes[0])
	}
	if nodes[1].Depth != 2 || nodes[1].Text != "B" {
		t.Fatalf("node 1: want {Depth:2 Text:B}, got %+v", nodes[1])
	}

	got, body, err := FindByPath(buf, "A > B")
	if err != nil {
		t.Fatalf("FindByPath A>B: unexpected err %v", err)
	}
	if got.Depth != 2 || got.Text != "B" {
		t.Fatalf("FindByPath A>B: want depth=2 text=B, got %+v", got)
	}
	if !strings.HasPrefix(string(body), "## B\n") {
		t.Fatalf("FindByPath A>B: body does not begin with heading line: %q", body)
	}
	if !strings.Contains(string(body), "body-b") {
		t.Fatalf("FindByPath A>B: body missing 'body-b': %q", body)
	}
}

// TestMdExplicitScan_DeepHeadingPath verifies a 3-segment path of
// strictly adjacent depths (1→2→3) resolves and reports the deepest
// node as the match target.
func TestMdExplicitScan_DeepHeadingPath(t *testing.T) {
	buf := []byte("# Top\ntop-body\n## Sub\nsub-body\n### Sub-sub\nleaf-body\n")

	nodes := WalkHeadings(buf)
	if len(nodes) != 3 {
		t.Fatalf("WalkHeadings: want 3 nodes, got %d", len(nodes))
	}
	wantTexts := []string{"Top", "Sub", "Sub-sub"}
	wantDepths := []int{1, 2, 3}
	for i, n := range nodes {
		if n.Depth != wantDepths[i] || n.Text != wantTexts[i] {
			t.Fatalf("node %d: want depth=%d text=%q, got depth=%d text=%q",
				i, wantDepths[i], wantTexts[i], n.Depth, n.Text)
		}
	}

	got, body, err := FindByPath(buf, "Top > Sub > Sub-sub")
	if err != nil {
		t.Fatalf("FindByPath deep: unexpected err %v", err)
	}
	if got.Depth != 3 || got.Text != "Sub-sub" {
		t.Fatalf("FindByPath deep: want depth=3 text=Sub-sub, got %+v", got)
	}
	if !strings.Contains(string(body), "leaf-body") {
		t.Fatalf("FindByPath deep: missing leaf-body: %q", body)
	}
}

// TestMdExplicitScan_SelectorGrammarPinned pins the selector grammar to
// " > " (space-greater-space) per amendment 1. Non-conforming separators
// MUST NOT match.
func TestMdExplicitScan_SelectorGrammarPinned(t *testing.T) {
	buf := []byte("# A\n## B\nbody\n")

	// Canonical separator works.
	if _, _, err := FindByPath(buf, "A > B"); err != nil {
		t.Fatalf("canonical ' > ' separator: unexpected err %v", err)
	}

	// Variant separators must NOT silently match.
	bad := []string{"A>B", "A  >  B", "A->B", "A/B", "A>>B"}
	for _, sel := range bad {
		_, _, err := FindByPath(buf, sel)
		if !errors.Is(err, format.ErrBlockNotFound) {
			t.Errorf("non-canonical separator %q: want ErrBlockNotFound, got %v", sel, err)
		}
	}

	// ParseSelector pin: " > " is the literal split point.
	got := ParseSelector("Introduction > Goals")
	want := []string{"Introduction", "Goals"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSelector: want %v, got %v", want, got)
	}
}

// TestMdExplicitScan_ManifestSelectorTranslation pins amendment 3:
// manifest selector strings are stored verbatim and translated at
// Find/Splice time (NOT at manifest-load). The translation here is
// ParseSelector splitting on " > " — no other processing.
func TestMdExplicitScan_ManifestSelectorTranslation(t *testing.T) {
	// Manifest stores selectors verbatim — including any unusual but
	// legal characters in heading text. Translation is purely the
	// split on " > ".
	cases := []struct {
		raw  string
		want []string
	}{
		{"Introduction > Goals", []string{"Introduction", "Goals"}},
		{"Top > Sub > Sub-sub", []string{"Top", "Sub", "Sub-sub"}},
		{"Just One", []string{"Just One"}},
		{"**Bold** > _Italic_", []string{"**Bold**", "_Italic_"}},
	}
	for _, c := range cases {
		got := ParseSelector(c.raw)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseSelector(%q): want %v, got %v", c.raw, c.want, got)
		}
	}

	// Empty selector returns nil and FindByPath surfaces ErrBlockNotFound.
	if got := ParseSelector(""); got != nil {
		t.Errorf("ParseSelector(\"\"): want nil, got %v", got)
	}
	if _, _, err := FindByPath([]byte("# A\n"), ""); !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("FindByPath empty selector: want ErrBlockNotFound, got %v", err)
	}
}

// TestMdExplicitScan_LevelGapUnmatched pins amendment 4: when a
// selector references a depth-N heading but the file skips levels
// (e.g. `# A` → `### B` with no `##`), the scanner treats the gap as
// unmatched and FindByPath returns format.ErrBlockNotFound.
func TestMdExplicitScan_LevelGapUnmatched(t *testing.T) {
	buf := []byte("# A\nintro\n### B\nbody\n")

	// Both headings are still scanned in the flat node list.
	nodes := WalkHeadings(buf)
	if len(nodes) != 2 {
		t.Fatalf("WalkHeadings: want 2 nodes, got %d", len(nodes))
	}
	if nodes[1].Depth != 3 {
		t.Fatalf("node 1 depth: want 3, got %d", nodes[1].Depth)
	}

	// Selector A > B expects depth-adjacent (1→2). File has 1→3.
	// Must return ErrBlockNotFound.
	_, _, err := FindByPath(buf, "A > B")
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Fatalf("FindByPath A>B across level gap: want ErrBlockNotFound, got %v", err)
	}

	// Direct depth-1 selector "A" still resolves.
	if _, _, err := FindByPath(buf, "A"); err != nil {
		t.Fatalf("FindByPath A: unexpected err %v", err)
	}
}

// TestMdExplicitScan_InlineFormattingBytesPreserved pins amendment 5:
// `## **Goals**` heading is matched ONLY by selector segment `**Goals**`,
// NOT by `Goals`. Scanner MUST preserve inline-formatting bytes literally.
func TestMdExplicitScan_InlineFormattingBytesPreserved(t *testing.T) {
	buf := []byte("# Intro\n## **Goals**\nbody\n")

	nodes := WalkHeadings(buf)
	if len(nodes) != 2 {
		t.Fatalf("WalkHeadings: want 2 nodes, got %d", len(nodes))
	}
	if nodes[1].Text != "**Goals**" {
		t.Fatalf("node 1 text: want %q, got %q", "**Goals**", nodes[1].Text)
	}

	// Literal selector matches.
	if _, _, err := FindByPath(buf, "Intro > **Goals**"); err != nil {
		t.Fatalf("literal selector: unexpected err %v", err)
	}

	// Stripped selector must NOT match.
	_, _, err := FindByPath(buf, "Intro > Goals")
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Fatalf("stripped selector: want ErrBlockNotFound, got %v", err)
	}

	// Underscore-italic variant.
	buf2 := []byte("## _Italic_\nbody\n")
	if _, _, err := FindByPath(buf2, "_Italic_"); err != nil {
		t.Fatalf("italic literal selector: unexpected err %v", err)
	}
	if _, _, err := FindByPath(buf2, "Italic"); !errors.Is(err, format.ErrBlockNotFound) {
		t.Fatalf("italic stripped selector: want ErrBlockNotFound, got %v", err)
	}
}

// TestMdExplicitScan_EscapedHashIgnored pins amendment 6: a line
// starting with "\#" is NOT a heading. This is its own test function
// (not a subtest) per amendment 6's wording.
func TestMdExplicitScan_EscapedHashIgnored(t *testing.T) {
	buf := []byte("\\# Not a heading\n# Real heading\nbody\n")

	nodes := WalkHeadings(buf)
	if len(nodes) != 1 {
		t.Fatalf("WalkHeadings: want 1 node, got %d (%+v)", len(nodes), nodes)
	}
	if nodes[0].Text != "Real heading" {
		t.Fatalf("node 0 text: want %q, got %q", "Real heading", nodes[0].Text)
	}

	// Selector targeting the escaped line MUST NOT match.
	if _, _, err := FindByPath(buf, "Not a heading"); !errors.Is(err, format.ErrBlockNotFound) {
		t.Fatalf("escaped heading must not match: got err %v", err)
	}
}

// TestMdExplicitScan_FenceAwareNoHeadingInsideFence pins amendment 8:
// heading-like lines inside ``` or ~~~ fences are NOT headings. Selector
// matching a fenced line returns format.ErrBlockNotFound.
func TestMdExplicitScan_FenceAwareNoHeadingInsideFence(t *testing.T) {
	t.Run("triple-backtick", func(t *testing.T) {
		buf := []byte("# Real\n```\n## Setup\nfenced body\n```\n## After\nafter-body\n")
		nodes := WalkHeadings(buf)
		// Expected: # Real, ## After. The ## Setup inside fence is skipped.
		if len(nodes) != 2 {
			t.Fatalf("want 2 nodes (fenced ## Setup excluded), got %d: %+v", len(nodes), nodes)
		}
		if nodes[0].Text != "Real" || nodes[1].Text != "After" {
			t.Fatalf("got nodes %+v", nodes)
		}
		if _, _, err := FindByPath(buf, "Real > Setup"); !errors.Is(err, format.ErrBlockNotFound) {
			t.Fatalf("Setup inside fence must not match: got err %v", err)
		}
		if _, _, err := FindByPath(buf, "Real > After"); err != nil {
			t.Fatalf("Real > After: unexpected err %v", err)
		}
	})

	t.Run("triple-tilde", func(t *testing.T) {
		buf := []byte("# Real\n~~~\n## Setup\nfenced body\n~~~\n## After\n")
		nodes := WalkHeadings(buf)
		if len(nodes) != 2 {
			t.Fatalf("want 2 nodes, got %d: %+v", len(nodes), nodes)
		}
		if nodes[1].Text != "After" {
			t.Fatalf("want node 1 After, got %+v", nodes[1])
		}
		if _, _, err := FindByPath(buf, "Real > Setup"); !errors.Is(err, format.ErrBlockNotFound) {
			t.Fatalf("Setup inside ~~~ fence must not match: got %v", err)
		}
	})

	t.Run("fence-info-string", func(t *testing.T) {
		// Fence with info string ("go", "markdown", etc) still opens a fence.
		buf := []byte("# Real\n```go\n## Setup\n```\n## After\n")
		nodes := WalkHeadings(buf)
		if len(nodes) != 2 {
			t.Fatalf("want 2 nodes with info-string fence, got %d: %+v", len(nodes), nodes)
		}
	})
}
