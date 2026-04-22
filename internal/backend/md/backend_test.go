package md

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/record"
)

// readmeTypes is the canonical dogfood schema for the README db used
// throughout this file: H1 "title" + H2 "section" only. H3+ headings
// in any buffer exercised below must flow through as body content per
// V2-PLAN §5.3.2.
var readmeTypes = []record.DeclaredType{
	{Name: "title", Heading: 1},
	{Name: "section", Heading: 2},
}

func newReadme(t *testing.T) *Backend {
	t.Helper()
	b, err := NewBackend(readmeTypes)
	if err != nil {
		t.Fatalf("NewBackend(readmeTypes): %v", err)
	}
	return b
}

func TestNewBackendRejectsBadLevel(t *testing.T) {
	// Out-of-range heading values are schema errors.
	badCases := []int{-1, 0, 7, 99}
	for _, lvl := range badCases {
		_, err := NewBackend([]record.DeclaredType{{Name: "x", Heading: lvl}})
		if err == nil {
			t.Errorf("NewBackend(Heading=%d): expected error", lvl)
		}
		if err != nil && !errors.Is(err, ErrBadLevel) {
			t.Errorf("NewBackend(Heading=%d): want ErrBadLevel, got %v", lvl, err)
		}
	}
	for lvl := 1; lvl <= 6; lvl++ {
		_, err := NewBackend([]record.DeclaredType{{Name: "x", Heading: lvl}})
		if err != nil {
			t.Errorf("NewBackend(Heading=%d): unexpected error %v", lvl, err)
		}
	}
}

// TestNewBackendRejectsDuplicateHeading covers the meta-schema rule
// (V2-PLAN §4.7): two declared types within one db must not share a
// heading level.
func TestNewBackendRejectsDuplicateHeading(t *testing.T) {
	_, err := NewBackend([]record.DeclaredType{
		{Name: "a", Heading: 2},
		{Name: "b", Heading: 2},
	})
	if err == nil {
		t.Fatal("expected ErrDuplicateHeading")
	}
	if !errors.Is(err, ErrDuplicateHeading) {
		t.Errorf("want ErrDuplicateHeading, got %v", err)
	}
}

// TestNewBackendAcceptsEmptyTypes shows the degenerate-but-valid case.
// A Backend with no declared types recognizes no records — List is
// empty, Find returns not-found — but construction succeeds.
func TestNewBackendAcceptsEmptyTypes(t *testing.T) {
	b, err := NewBackend(nil)
	if err != nil {
		t.Fatalf("NewBackend(nil): %v", err)
	}
	src := []byte("# ta\n\n## Installation\n\nbody\n")
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list with no declared types, got %v", got)
	}
	_, ok, err := b.Find(src, "section.installation")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ok {
		t.Error("expected Find not-found with no declared types")
	}
}

// TestListEmitsRelativeAddresses verifies List returns
// "<type-name>.<slug>" relative addresses. The caller (resolver) glues
// the "<db>" / "<db>.<instance>" prefix on as the db shape requires.
func TestListEmitsRelativeAddresses(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## Installation\n\nbody\n\n## MCP client config\n\nc\n")
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"title.ta", "section.installation", "section.mcp-client-config"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestListWithScopePrefix filters addresses against a prefix. The
// higher layer uses this for "<db>.<type>" / id-prefix scopes.
func TestListWithScopePrefix(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## Installation\n\nx\n\n## MCP client config\n\ny\n")
	got, err := b.List(src, "section")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"section.installation", "section.mcp-client-config"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// TestSchemaDrivenSectioningH3AsBody is the §5.3.2 worked example.
// Schema declares H1 "title" + H2 "section" and nothing deeper. H3
// "Prerequisites" and H3 "Troubleshooting" live inside the H2
// "Installation" body and must survive as content, not become
// sibling records.
func TestSchemaDrivenSectioningH3AsBody(t *testing.T) {
	src := []byte(`# ta

Tiny MCP server for schema-validated TOML and Markdown.

## Installation

Install from source:

    mage install

### Prerequisites

A Go toolchain.

### Troubleshooting

If mage install fails, ...

## MCP client config

...
`)
	b := newReadme(t)

	// List surfaces only H1 title + the two H2s. Neither H3 is a
	// section boundary.
	addrs, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"title.ta", "section.installation", "section.mcp-client-config"}
	if !reflect.DeepEqual(addrs, want) {
		t.Errorf("List got %v, want %v", addrs, want)
	}

	// Find on the H2 "Installation" returns a byte range that
	// absorbs both H3s.
	sec, ok, err := b.Find(src, "section.installation")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("section.installation not found")
	}
	span := string(src[sec.Range[0]:sec.Range[1]])
	if !strings.HasPrefix(span, "## Installation") {
		t.Errorf("span should start at H2 Installation, got prefix %q", span[:50])
	}
	for _, want := range []string{"### Prerequisites", "### Troubleshooting", "A Go toolchain.", "mage install"} {
		if !strings.Contains(span, want) {
			t.Errorf("span should contain %q, got %q", want, span)
		}
	}
	if strings.Contains(span, "## MCP client config") {
		t.Errorf("span should stop before next H2, got %q", span)
	}
}

// TestCrossLevelSameSlugNoCollision covers V2-PLAN §5.3.2:
// slug-uniqueness is per declared level. H1 "ta" and H2 "ta" are
// different addresses at different declared types and MUST NOT
// collide. Non-declared levels don't participate at all.
func TestCrossLevelSameSlugNoCollision(t *testing.T) {
	src := []byte("# ta\n\n## ta\n\nbody\n")
	b := newReadme(t)
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"title.ta", "section.ta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestWithinLevelSlugCollisionErrors: two H2s with the same slug when
// H2 is declared — collision at read time.
func TestWithinLevelSlugCollisionErrors(t *testing.T) {
	src := []byte("## Install\nbody1\n## Install\nbody2\n")
	b := newReadme(t)
	if _, err := b.List(src, ""); !errors.Is(err, ErrSlugCollision) {
		t.Errorf("expected ErrSlugCollision, got %v", err)
	}
}

// TestNonDeclaredLevelCollisionsIgnored: two H3s with the same slug,
// but H3 is not declared → they're content → no collision error.
func TestNonDeclaredLevelCollisionsIgnored(t *testing.T) {
	src := []byte("## Install\n\n### Dup\ntext\n\n### Dup\ntext\n")
	b := newReadme(t)
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List (expected no error): %v", err)
	}
	if len(got) != 1 || got[0] != "section.install" {
		t.Errorf("got %v, want [section.install]", got)
	}
}

func TestFindReturnsByteRange(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## Installation\n\nInstall from source.\n\n## Next\n\nmore\n")
	got, ok, err := b.Find(src, "section.installation")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("section not found")
	}
	if got.Path != "section.installation" {
		t.Errorf("Path = %q", got.Path)
	}
	snippet := string(src[got.Range[0]:got.Range[1]])
	if !strings.HasPrefix(snippet, "## Installation") {
		t.Errorf("snippet prefix wrong: %q", snippet)
	}
	if !strings.Contains(snippet, "Install from source.") {
		t.Errorf("snippet body missing: %q", snippet)
	}
	if strings.Contains(snippet, "## Next") {
		t.Errorf("snippet leaked into next section: %q", snippet)
	}
}

// TestFindDbPrefixedAddress confirms Find accepts addresses with
// extra leading segments ("<db>.<instance>.<type>.<slug>"); the tail
// "<type>.<slug>" is what the backend uses.
func TestFindDbPrefixedAddress(t *testing.T) {
	b := newReadme(t)
	src := []byte("## Installation\n\nbody\n")
	_, ok, err := b.Find(src, "readme.section.installation")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("prefixed address should find")
	}
	// And with db.instance prefix:
	_, ok, err = b.Find(src, "docs.foo.section.installation")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("db.instance prefixed address should find")
	}
}

func TestFindMissing(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## Installation\n\nx\n")
	_, ok, err := b.Find(src, "section.does-not-exist")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ok {
		t.Error("expected not found")
	}
}

func TestFindEmptyPathErrors(t *testing.T) {
	b := newReadme(t)
	if _, _, err := b.Find([]byte("# x\n"), ""); err == nil {
		t.Error("expected error on empty path")
	}
}

func TestEmitBodyOnly(t *testing.T) {
	b := newReadme(t)
	got, err := b.Emit("section.installation", record.Record{
		"body": "Install from source:\n\n    mage install\n",
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	want := "## Installation\n\nInstall from source:\n\n    mage install\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestEmitNoBodyStillRendersHeading(t *testing.T) {
	b := newReadme(t)
	got, err := b.Emit("section.todo", record.Record{})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.HasPrefix(got, []byte("## Todo\n")) {
		t.Errorf("got %q", got)
	}
}

func TestEmitEnsuresTrailingNewline(t *testing.T) {
	b, err := NewBackend([]record.DeclaredType{{Name: "foo", Heading: 3}})
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	got, err := b.Emit("x.y.foo.bar", record.Record{"body": "no newline at end"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got[len(got)-1] != '\n' {
		t.Errorf("no trailing newline: %q", got)
	}
}

// TestEmitRejectsNonDeclaredType covers the address / declared-type
// contract: if the address's type-name segment doesn't match any
// declared type, Emit has no way to pick a heading level and must
// error with ErrNotDeclaredType.
func TestEmitRejectsNonDeclaredType(t *testing.T) {
	b := newReadme(t)
	_, err := b.Emit("readme.nosuchtype.slug", record.Record{"body": "x"})
	if err == nil {
		t.Fatal("expected error for non-declared type")
	}
	if !errors.Is(err, ErrNotDeclaredType) {
		t.Errorf("want ErrNotDeclaredType, got %v", err)
	}
}

func TestEmitRejectsEmptySection(t *testing.T) {
	b := newReadme(t)
	if _, err := b.Emit("", record.Record{"body": "x"}); err == nil {
		t.Error("expected error on empty section")
	}
	if _, err := b.Emit("", record.Record{"body": "x"}); err != nil && !errors.Is(err, ErrEmptySection) {
		t.Errorf("want ErrEmptySection, got %v", err)
	}
}

func TestUnslugifyForHeading(t *testing.T) {
	cases := []struct{ in, want string }{
		{"installation", "Installation"},
		{"mcp-client-config", "Mcp Client Config"},
		{"getting-started", "Getting Started"},
		{"single", "Single"},
	}
	for _, tc := range cases {
		if got := unslugifyForHeading(tc.in); got != tc.want {
			t.Errorf("unslugifyForHeading(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSpliceReplaceExisting(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## Installation\n\nold\n\n## Next\n\nkeep\n")
	emitted := []byte("## Installation\n\nnew body\n")
	out, err := b.Splice(src, "section.installation", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Contains(out, []byte("new body")) {
		t.Errorf("new body missing: %q", out)
	}
	if bytes.Contains(out, []byte("\nold\n")) {
		t.Errorf("old body leaked: %q", out)
	}
	if !bytes.Contains(out, []byte("## Next")) {
		t.Errorf("next section wiped: %q", out)
	}
	if !bytes.HasPrefix(out, []byte("# ta\n")) {
		t.Errorf("leading H1 missing: %q", out)
	}
}

// TestSpliceAbsorbsNonDeclaredChildOnReplace locks in the §5.3.2
// worked-example rule through Splice: when we replace the H2
// "Installation" that held H3 "Prerequisites" + H3 "Troubleshooting"
// in its body, the H3s vanish (they were body content of the H2 being
// replaced). The next declared H2 must survive intact.
func TestSpliceAbsorbsNonDeclaredChildOnReplace(t *testing.T) {
	src := []byte(`## Installation

Install from source:

### Prerequisites

A Go toolchain.

### Troubleshooting

If mage install fails, ...

## MCP client config

cfg
`)
	b := newReadme(t)
	emitted := []byte("## Installation\n\nBrand new body.\n")
	out, err := b.Splice(src, "section.installation", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Contains(out, []byte("Brand new body.")) {
		t.Errorf("new body missing: %q", out)
	}
	if bytes.Contains(out, []byte("### Prerequisites")) {
		t.Errorf("absorbed H3 should be replaced: %q", out)
	}
	if bytes.Contains(out, []byte("### Troubleshooting")) {
		t.Errorf("absorbed H3 should be replaced: %q", out)
	}
	if !bytes.Contains(out, []byte("## MCP client config\n\ncfg\n")) {
		t.Errorf("next declared H2 must survive: %q", out)
	}
}

func TestSpliceAppendWhenMissing(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## A\n\nbody-a\n")
	emitted := []byte("## B\n\nbody-b\n")
	out, err := b.Splice(src, "section.b", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Contains(out, []byte("## A")) || !bytes.Contains(out, []byte("## B")) {
		t.Errorf("missing sections: %q", out)
	}
	if !bytes.HasSuffix(out, []byte("body-b\n")) {
		t.Errorf("B not at end: %q", out)
	}
}

func TestSpliceAppendToEmpty(t *testing.T) {
	b := newReadme(t)
	emitted := []byte("# title\n\nbody\n")
	out, err := b.Splice(nil, "title.title", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if string(out) != "# title\n\nbody\n" {
		t.Errorf("got %q", out)
	}
}

func TestSpliceAppendEnsuresSeparator(t *testing.T) {
	b := newReadme(t)
	// Buffer ends without trailing newline.
	src := []byte("# ta\n\n## A\n\nbody-a")
	emitted := []byte("## B\n\nbody-b\n")
	out, err := b.Splice(src, "section.b", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Contains(out, []byte("body-a\n\n## B")) {
		t.Errorf("separator missing between existing content and appended section: %q", out)
	}
}

func TestSpliceRoundTripInvariant(t *testing.T) {
	// Emitting then splicing with the same body should produce
	// byte-identical output (modulo trailing-newline normalisation).
	b := newReadme(t)
	src := []byte("# ta\n\n## Installation\n\nInstall from source.\n\n## Next\n\nMore.\n")
	sec, ok, err := b.Find(src, "section.installation")
	if err != nil || !ok {
		t.Fatalf("Find: ok=%v err=%v", ok, err)
	}
	orig := src[sec.Range[0]:sec.Range[1]]
	// Extract body (skip the heading line).
	firstNL := bytes.IndexByte(orig, '\n')
	body := string(orig[firstNL+1:])
	// Trim the leading blank line from body so Emit's own "\n\n" shim works.
	body = strings.TrimPrefix(body, "\n")

	emitted, err := b.Emit("section.installation", record.Record{"body": body})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out, err := b.Splice(src, "section.installation", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Equal(src, out) {
		t.Errorf("round-trip not byte-identical:\n--- src ---\n%s\n--- out ---\n%s", src, out)
	}
}

func TestSpliceEmptyReplacementRejected(t *testing.T) {
	b := newReadme(t)
	src := []byte("# x\n")
	if _, err := b.Splice(src, "section.x", nil); err == nil {
		t.Error("expected error on empty replacement")
	}
}

func TestSpliceEmptyPathRejected(t *testing.T) {
	b := newReadme(t)
	src := []byte("# x\n")
	if _, err := b.Splice(src, "", []byte("## X\n")); err == nil {
		t.Error("expected error on empty path")
	}
}

func TestCollisionPropagatesThroughListAndFind(t *testing.T) {
	b := newReadme(t)
	src := []byte("## Dup\n\nbody1\n## Dup\n\nbody2\n")
	if _, err := b.List(src, ""); !errors.Is(err, ErrSlugCollision) {
		t.Errorf("List: expected ErrSlugCollision, got %v", err)
	}
	if _, _, err := b.Find(src, "section.dup"); !errors.Is(err, ErrSlugCollision) {
		t.Errorf("Find: expected ErrSlugCollision, got %v", err)
	}
}
