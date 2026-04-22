package md

import (
	"reflect"
	"testing"
)

// allLevels is a "declare every ATX level" set so that tests that care
// only about heading recognition (not schema-driven filtering) behave
// the way pre-refactor tests expected.
var allLevels = map[int]struct{}{1: {}, 2: {}, 3: {}, 4: {}, 5: {}, 6: {}}

// stripHeadings returns just the text and level for equality comparisons
// where byte offsets aren't the focus.
type headingBrief struct {
	Level int
	Text  string
	Slug  string
}

func brief(hs []Heading) []headingBrief {
	out := make([]headingBrief, len(hs))
	for i, h := range hs {
		out[i] = headingBrief{Level: h.Level, Text: h.Text, Slug: h.Slug}
	}
	return out
}

func TestScanATXBasic(t *testing.T) {
	src := []byte("# ta\n\nIntro.\n\n## Installation\n\nInstall.\n\n## MCP client config\n\nConfig.\n")
	got, err := scanATX(src, allLevels)
	if err != nil {
		t.Fatalf("scanATX: %v", err)
	}
	want := []headingBrief{
		{Level: 1, Text: "ta", Slug: "ta"},
		{Level: 2, Text: "Installation", Slug: "installation"},
		{Level: 2, Text: "MCP client config", Slug: "mcp-client-config"},
	}
	if !reflect.DeepEqual(brief(got), want) {
		t.Errorf("got %+v\nwant %+v", brief(got), want)
	}
}

func TestScanATXRequiresColZero(t *testing.T) {
	// Leading spaces before # are NOT a heading (strict col-0 policy).
	src := []byte("   # Not a heading\n\n# Real heading\n")
	got, err := scanATX(src, allLevels)
	if err != nil {
		t.Fatalf("scanATX: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d headings, want 1", len(got))
	}
	if got[0].Text != "Real heading" {
		t.Errorf("heading text = %q", got[0].Text)
	}
}

func TestScanATXTrailingHashesStripped(t *testing.T) {
	src := []byte("## Installation ##\n\nbody\n")
	got, err := scanATX(src, allLevels)
	if err != nil {
		t.Fatalf("scanATX: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d headings, want 1", len(got))
	}
	if got[0].Text != "Installation" {
		t.Errorf("trailing hashes not stripped: %q", got[0].Text)
	}
}

func TestScanATXLevels(t *testing.T) {
	src := []byte("# H1\n## H2\n### H3\n#### H4\n##### H5\n###### H6\n")
	got, err := scanATX(src, allLevels)
	if err != nil {
		t.Fatalf("scanATX: %v", err)
	}
	for i, h := range got {
		if h.Level != i+1 {
			t.Errorf("got[%d].Level = %d, want %d", i, h.Level, i+1)
		}
	}
}

func TestScanATXSevenHashesIgnored(t *testing.T) {
	// 7+ hashes are not valid ATX headings.
	src := []byte("####### too many\n\n# real\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 1 || got[0].Text != "real" {
		t.Errorf("got %+v, want only 'real'", brief(got))
	}
}

func TestScanATXFencedCodeBlockHides(t *testing.T) {
	src := []byte("# Real\n\n" +
		"```go\n" +
		"# not a heading inside fence\n" +
		"## also not\n" +
		"```\n" +
		"\n## After fence\n")
	got, err := scanATX(src, allLevels)
	if err != nil {
		t.Fatalf("scanATX: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d headings, want 2 (%+v)", len(got), brief(got))
	}
	if got[0].Text != "Real" || got[1].Text != "After fence" {
		t.Errorf("headings wrong: %+v", brief(got))
	}
}

func TestScanATXTildeFence(t *testing.T) {
	src := []byte("# Real\n\n~~~\n# hidden\n~~~\n\n## After\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(got), brief(got))
	}
}

func TestScanATXFenceLongerOpenerNeededToClose(t *testing.T) {
	// A ~~~ run inside a ~~~~ fence is content, not close.
	src := []byte("# Real\n\n~~~~\n# hidden 1\n~~~\nstill inside\n# hidden 2\n~~~~\n\n## After\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (%+v)", len(got), brief(got))
	}
	if got[1].Text != "After" {
		t.Errorf("second heading = %q", got[1].Text)
	}
}

func TestScanATXSetextIgnored(t *testing.T) {
	src := []byte("Not a heading\n===========\n\n# real\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 1 || got[0].Text != "real" {
		t.Errorf("got %+v, want only 'real'", brief(got))
	}
}

func TestScanATXByteRangeCoversToNextHeading(t *testing.T) {
	src := []byte("# a\nbody-of-a\n## b\nbody-of-b\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 2 {
		t.Fatalf("got %d headings", len(got))
	}
	// First heading's byte range should span through the start of "## b".
	end := got[0].ByteRange[1]
	if string(src[got[0].ByteRange[0]:end]) != "# a\nbody-of-a\n" {
		t.Errorf("first range = %q", src[got[0].ByteRange[0]:end])
	}
	// Second heading spans to EOF.
	if got[1].ByteRange[1] != len(src) {
		t.Errorf("second end = %d, want %d", got[1].ByteRange[1], len(src))
	}
}

// TestScanATXNonDeclaredLevelIsContent is the schema-driven-sectioning
// unit test: H3 and deeper are not declared, so their byte ranges are
// absorbed into the H2's body and they do not appear in scanATX output.
func TestScanATXNonDeclaredLevelIsContent(t *testing.T) {
	src := []byte("# ta\n\n## Installation\n\n### Prerequisites\n\nA Go toolchain.\n\n### Troubleshooting\n\nsomething\n\n## MCP config\n\nc\n")
	declared := map[int]struct{}{1: {}, 2: {}}
	got, err := scanATX(src, declared)
	if err != nil {
		t.Fatalf("scanATX: %v", err)
	}
	want := []headingBrief{
		{Level: 1, Text: "ta", Slug: "ta"},
		{Level: 2, Text: "Installation", Slug: "installation"},
		{Level: 2, Text: "MCP config", Slug: "mcp-config"},
	}
	if !reflect.DeepEqual(brief(got), want) {
		t.Errorf("got %+v\nwant %+v", brief(got), want)
	}
	// Installation byte range must extend from "## Installation" all
	// the way to "## MCP config".
	installationIdx := 1
	span := string(src[got[installationIdx].ByteRange[0]:got[installationIdx].ByteRange[1]])
	for _, want := range []string{"### Prerequisites", "### Troubleshooting", "A Go toolchain."} {
		if !contains(span, want) {
			t.Errorf("installation span missing %q: %q", want, span)
		}
	}
	if contains(span, "## MCP config") {
		t.Errorf("installation span should stop before MCP config, got %q", span)
	}
}

// TestScanATXEmptyDeclaredLevelsYieldsNoHeadings: with no declared
// levels, scanATX must return no headings regardless of the buffer.
func TestScanATXEmptyDeclaredLevelsYieldsNoHeadings(t *testing.T) {
	src := []byte("# A\n## B\n### C\n")
	got, err := scanATX(src, map[int]struct{}{})
	if err != nil {
		t.Fatalf("scanATX: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 headings with no declared levels, got %d: %+v", len(got), brief(got))
	}
}

func TestScanATXEmptyHeadingIgnored(t *testing.T) {
	// "# " with nothing after the space is not a valid heading — blank text.
	src := []byte("# \n# Real\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 1 || got[0].Text != "Real" {
		t.Errorf("got %+v, want only 'Real'", brief(got))
	}
}

func TestScanATXRequiresSpaceAfterHashes(t *testing.T) {
	// "#Heading" (no space) is not an ATX heading.
	src := []byte("#noSpace\n# ok\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 1 || got[0].Text != "ok" {
		t.Errorf("got %+v, want only 'ok'", brief(got))
	}
}

func TestScanATXTabAfterHashesAccepted(t *testing.T) {
	src := []byte("#\tTabbed\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 1 || got[0].Text != "Tabbed" {
		t.Errorf("got %+v, want 'Tabbed'", brief(got))
	}
}

func TestScanATXConsecutiveHeadings(t *testing.T) {
	src := []byte("# a\n## b\n### c\n")
	got, _ := scanATX(src, allLevels)
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
	// Each byte range should be exactly one line: the heading line itself.
	for i, h := range got {
		line := string(src[h.ByteRange[0]:h.ByteRange[1]])
		if len(line) == 0 {
			t.Errorf("heading %d has empty range", i)
		}
	}
}

func TestScanATXSlugCollisionAtSameLevel(t *testing.T) {
	src := []byte("## Installation\nbody 1\n## Installation\nbody 2\n")
	_, err := scanATX(src, allLevels)
	if err == nil {
		t.Fatal("expected ErrSlugCollision")
	}
}

func TestScanATXNoCollisionAcrossLevels(t *testing.T) {
	// Same slug but different levels — not a collision per §5.3.2.
	src := []byte("# Installation\nbody 1\n## Installation\nbody 2\n")
	_, err := scanATX(src, allLevels)
	if err != nil {
		t.Errorf("cross-level same slug should not collide: %v", err)
	}
}

func TestSlugFromHeading(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Installation", "installation"},
		{"MCP client config", "mcp-client-config"},
		{"Getting Started", "getting-started"},
		{"  Whitespace  ", "whitespace"},
		{"Already-Kebab", "already-kebab"},
	}
	for _, tc := range cases {
		if got := slugFromHeading(tc.in); got != tc.want {
			t.Errorf("slugFromHeading(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// contains is a tiny strings.Contains alias so this file doesn't drag
// in the strings package just for one test helper.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
