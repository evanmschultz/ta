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

// TestListEmitsRelativeAddresses verifies List returns hierarchical
// "<type-name>.<chain>" relative addresses. The caller (resolver) glues
// the "<db>" / "<db>.<instance>" prefix on as the db shape requires.
// With both H1 "title" and H2 "section" declared, each H2's chain
// includes the H1 ancestor slug — hence "section.ta.installation" and
// not "section.installation".
func TestListEmitsRelativeAddresses(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## Installation\n\nbody\n\n## MCP client config\n\nc\n")
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"title.ta", "section.ta.installation", "section.ta.mcp-client-config"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestListWithScopePrefix filters addresses against a segment-aligned
// prefix. Under the hierarchical refinement, scope "section" (without
// a chain) does not match any concrete address — addresses carry the
// H1 ancestor slug. Scope "section.ta" returns every H2 under H1 ta.
func TestListWithScopePrefix(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## Installation\n\nx\n\n## MCP client config\n\ny\n")
	got, err := b.List(src, "section.ta")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"section.ta.installation", "section.ta.mcp-client-config"}
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
	// section boundary. H2 addresses include the H1 ancestor slug per
	// the hierarchical refinement.
	addrs, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"title.ta", "section.ta.installation", "section.ta.mcp-client-config"}
	if !reflect.DeepEqual(addrs, want) {
		t.Errorf("List got %v, want %v", addrs, want)
	}

	// Find on the H2 "Installation" returns a byte range that
	// absorbs both H3s.
	sec, ok, err := b.Find(src, "section.ta.installation")
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

// TestCrossLevelSameSlugNoCollision covers V2-PLAN §5.3.2 (2026-04-21
// hierarchical refinement): H1 "ta" and H2 "ta" are different declared
// types at different levels. Under the hierarchical addressing rule
// the H2's address includes its H1 ancestor's slug in the chain, so
// the two addresses differ (title.ta vs section.ta.ta) and there is
// no collision.
func TestCrossLevelSameSlugNoCollision(t *testing.T) {
	src := []byte("# ta\n\n## ta\n\nbody\n")
	b := newReadme(t)
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"title.ta", "section.ta.ta"}
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
	got, ok, err := b.Find(src, "section.ta.installation")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("section not found")
	}
	if got.Path != "section.ta.installation" {
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
// extra leading segments ("<db>.<instance>.<type>.<chain>"); the
// "<type>.<chain>" tail is what the backend matches.
func TestFindDbPrefixedAddress(t *testing.T) {
	b := newReadme(t)
	// No H1 in buf — H2's chain has no declared-ancestor slug.
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
	_, ok, err := b.Find(src, "section.ta.does-not-exist")
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
	out, err := b.Splice(src, "section.ta.installation", emitted)
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
	// No H1 in src, so H2 Installation's address has no ancestor slug.
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

// TestSpliceInsertsUnderExistingParent: when the target H2 address
// does not yet exist but its declared H1 ancestor does, splice inserts
// the new record at the end of the parent's body range (V2-PLAN §5.3.2
// / §11.D #3). The existing H2 sibling stays intact.
func TestSpliceInsertsUnderExistingParent(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\n## A\n\nbody-a\n")
	emitted := []byte("## B\n\nbody-b\n")
	out, err := b.Splice(src, "section.ta.b", emitted)
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
	// Buffer ends without trailing newline. H2 is inserted at end of
	// its H1 parent's body range (which runs to EOF).
	src := []byte("# ta\n\n## A\n\nbody-a")
	emitted := []byte("## B\n\nbody-b\n")
	out, err := b.Splice(src, "section.ta.b", emitted)
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
	sec, ok, err := b.Find(src, "section.ta.installation")
	if err != nil || !ok {
		t.Fatalf("Find: ok=%v err=%v", ok, err)
	}
	orig := src[sec.Range[0]:sec.Range[1]]
	// Extract body (skip the heading line).
	firstNL := bytes.IndexByte(orig, '\n')
	body := string(orig[firstNL+1:])
	// Trim the leading blank line from body so Emit's own "\n\n" shim works.
	body = strings.TrimPrefix(body, "\n")

	emitted, err := b.Emit("section.ta.installation", record.Record{"body": body})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out, err := b.Splice(src, "section.ta.installation", emitted)
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

// TestHierarchicalAddressingH1H2H3 covers the full V2-PLAN §5.3.2
// hierarchical addressing example: with H1 "title", H2 "section", and
// H3 "subsection" all declared, an H3 under an H2 under an H1 resolves
// to "subsection.<h1-slug>.<h2-slug>.<h3-slug>".
func TestHierarchicalAddressingH1H2H3(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "title", Heading: 1},
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("# ta\n\n## Install\n\n### Prereqs\n\nbody\n\n### Setup\n\nbody\n\n## Config\n\nc\n")
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{
		"title.ta",
		"section.ta.install",
		"subsection.ta.install.prereqs",
		"subsection.ta.install.setup",
		"section.ta.config",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// TestHierarchicalAddressingH2H3OnlyNoH1Declared: when H1 is not
// declared the chain starts at the shallowest DECLARED ancestor. The
// H1 "ta" heading is non-declared content; H2 addresses start at the
// H2 slug and H3 addresses start at "<h2-slug>.<h3-slug>".
func TestHierarchicalAddressingH2H3OnlyNoH1Declared(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("# ta\n\n## Install\n\n### Prereqs\n\nbody\n\n## Config\n\nc\n")
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{
		"section.install",
		"subsection.install.prereqs",
		"section.config",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// TestPerParentSlugUniquenessAllowsCrossParentDuplicates: H3 "Prereqs"
// under H2 "Install" and another H3 "Prereqs" under H2 "Config" must
// NOT collide — they have different parent chains and therefore
// different full addresses.
func TestPerParentSlugUniquenessAllowsCrossParentDuplicates(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("## Install\n\n### Prereqs\n\nb1\n\n## Config\n\n### Prereqs\n\nb2\n")
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List (expected no collision): %v", err)
	}
	want := []string{
		"section.install",
		"subsection.install.prereqs",
		"section.config",
		"subsection.config.prereqs",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// TestPerParentSlugCollisionUnderSameParentErrors: two H3 "Prereqs"
// under the same H2 DO collide — same parent chain + same slug = same
// full address.
func TestPerParentSlugCollisionUnderSameParentErrors(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("## Install\n\n### Prereqs\n\nb1\n\n### Prereqs\n\nb2\n")
	if _, err := b.List(src, ""); !errors.Is(err, ErrSlugCollision) {
		t.Errorf("expected ErrSlugCollision for duplicate subsection under same section, got %v", err)
	}
}

// TestGetParentReturnsSubtreeIncludingChildren: Find on a parent H2
// returns bytes including nested H3s (they are body bytes of the H2
// AND addressable records with their own narrower ranges).
func TestGetParentReturnsSubtreeIncludingChildren(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("## Install\n\nintro\n\n### Prereqs\n\ntoolchain\n\n### Setup\n\nsteps\n\n## Config\n\nc\n")
	sec, ok, err := b.Find(src, "section.install")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("section.install not found")
	}
	span := string(src[sec.Range[0]:sec.Range[1]])
	if !strings.HasPrefix(span, "## Install") {
		t.Errorf("span should start at H2 Install, got %q", span[:40])
	}
	for _, want := range []string{"### Prereqs", "### Setup", "toolchain", "steps"} {
		if !strings.Contains(span, want) {
			t.Errorf("span should contain %q, got %q", want, span)
		}
	}
	if strings.Contains(span, "## Config") {
		t.Errorf("span must stop before next H2, got %q", span)
	}
}

// TestGetChildReturnsNestedRange: Find on the child H3 returns just the
// H3 block, nested inside the parent H2's range.
func TestGetChildReturnsNestedRange(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("## Install\n\nintro\n\n### Prereqs\n\ntoolchain\n\n### Setup\n\nsteps\n\n## Config\n\nc\n")
	parent, _, err := b.Find(src, "section.install")
	if err != nil {
		t.Fatalf("Find parent: %v", err)
	}
	child, ok, err := b.Find(src, "subsection.install.prereqs")
	if err != nil {
		t.Fatalf("Find child: %v", err)
	}
	if !ok {
		t.Fatal("subsection.install.prereqs not found")
	}
	span := string(src[child.Range[0]:child.Range[1]])
	if !strings.HasPrefix(span, "### Prereqs") {
		t.Errorf("child span should start at H3 Prereqs, got %q", span)
	}
	if !strings.Contains(span, "toolchain") {
		t.Errorf("child span body missing: %q", span)
	}
	if strings.Contains(span, "### Setup") {
		t.Errorf("child range must end at next same-or-shallower heading, got %q", span)
	}
	// Child range must nest inside parent range.
	if child.Range[0] < parent.Range[0] || child.Range[1] > parent.Range[1] {
		t.Errorf("child %v must nest inside parent %v", child.Range, parent.Range)
	}
}

// TestEmitDeclaredChildErrorsIfParentMissing: splicing a new H3 whose
// H2 ancestor is not present in buf returns ErrParentMissing rather
// than silently inventing an H2 or appending at EOF.
func TestEmitDeclaredChildErrorsIfParentMissing(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("## Other\n\nbody\n")
	emitted := []byte("### Prereqs\n\ntoolchain\n")
	_, err = b.Splice(src, "subsection.install.prereqs", emitted)
	if err == nil {
		t.Fatal("expected ErrParentMissing; got nil")
	}
	if !errors.Is(err, ErrParentMissing) {
		t.Errorf("want ErrParentMissing, got %v", err)
	}
}

// TestSpliceInsertsChildAtEndOfParentRange: when the H2 parent exists
// but the target H3 child does not, Splice inserts the child at the
// end of the parent's body range (just before the next same-or-shallower
// declared heading, or EOF).
func TestSpliceInsertsChildAtEndOfParentRange(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("## Install\n\nintro\n\n### Existing\n\nbody\n\n## Config\n\nc\n")
	emitted := []byte("### New\n\nnewbody\n")
	out, err := b.Splice(src, "subsection.install.new", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	// The new H3 must appear BEFORE the next H2 Config (which is the
	// parent Install's range end).
	newIdx := bytes.Index(out, []byte("### New"))
	configIdx := bytes.Index(out, []byte("## Config"))
	if newIdx < 0 || configIdx < 0 {
		t.Fatalf("missing heading in output: %q", out)
	}
	if newIdx >= configIdx {
		t.Errorf("new child should land before ## Config: got %q", out)
	}
	// Existing sibling and config must survive.
	for _, want := range []string{"### Existing", "## Config", "newbody"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

// TestRangeEndsAtSameOrShallowerDeclaredLevel: scanner-level boundary
// check. Under H1+H2+H3 declared, an H2's range ends at the next H2 OR
// H1 (whichever comes first), not at the next H3.
func TestRangeEndsAtSameOrShallowerDeclaredLevel(t *testing.T) {
	declared := map[int]string{1: "title", 2: "section", 3: "subsection"}
	src := []byte("## A\n\nabody\n\n### A1\n\na1body\n\n## B\n\nbbody\n")
	got, err := scanATX(src, declared)
	if err != nil {
		t.Fatalf("scanATX: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d headings, want 3: %+v", len(got), brief(got))
	}
	// H2 A ends at H2 B.
	aSpan := string(src[got[0].ByteRange[0]:got[0].ByteRange[1]])
	if !strings.Contains(aSpan, "### A1") || strings.Contains(aSpan, "## B") {
		t.Errorf("H2 A range wrong: %q", aSpan)
	}
	// H3 A1 ends at H2 B (shallower than H3).
	a1Span := string(src[got[1].ByteRange[0]:got[1].ByteRange[1]])
	if !strings.Contains(a1Span, "a1body") || strings.Contains(a1Span, "## B") {
		t.Errorf("H3 A1 range wrong: %q", a1Span)
	}
	// H2 B runs to EOF.
	if got[2].ByteRange[1] != len(src) {
		t.Errorf("H2 B end = %d, want %d", got[2].ByteRange[1], len(src))
	}
}

// TestOrphanH3UnderH1WithMissingH2 covers V2-PLAN §11.D pre-answered
// ambiguity #1: an H3 directly under an H1 when H2 is declared but no
// H2 heading exists in buf. The H3 is scanned; its chain skips the
// missing H2 slot and contains only declared ancestors that ARE
// present. Address = "subsection.<h1-slug>.<h3-slug>".
func TestOrphanH3UnderH1WithMissingH2(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "title", Heading: 1},
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("# ta\n\n### Prereqs\n\nA Go toolchain.\n")
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"title.ta", "subsection.ta.prereqs"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// TestSpliceRejectsMalformedAddress covers the Splice-side
// counterpart of the Emit malformed-address guard. Splice must reject
// the same addresses Emit rejects so the two entry points share one
// contract (V2-PLAN §5.3.2 / §5.1). A bare type segment with no slug
// ("readme.title" when "title" is declared) has no heading to insert
// against and must error ErrMalformedSection, not silently append.
func TestSpliceRejectsMalformedAddress(t *testing.T) {
	b := newReadme(t)
	src := []byte("# ta\n\nprose\n")
	_, err := b.Splice(src, "readme.title", []byte("# new\n"))
	if !errors.Is(err, ErrMalformedSection) {
		t.Fatalf("Splice with bare type segment: got %v, want ErrMalformedSection", err)
	}
}

// TestSpliceOrphanSiblingCreationRejected locks in the strict-orphan
// write semantics from V2-PLAN §5.3.2 orphans paragraph. An orphan H3
// exists under an H1 with H2 declared-but-missing; a NEW H3 sibling
// at that same orphan level must fail ErrParentMissing because the
// declared parent (section.ta) is not present in the buffer. The
// caller's recovery is to create the H2 first, then retry the H3.
func TestSpliceOrphanSiblingCreationRejected(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "title", Heading: 1},
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("# ta\n\n### Quick start\n\nRun mage install.\n")
	emitted := []byte("### Troubleshooting\n\nCheck Go version.\n")
	out, err := b.Splice(src, "subsection.ta.troubleshooting", emitted)
	if !errors.Is(err, ErrParentMissing) {
		t.Fatalf("Splice orphan sibling: got %v, want ErrParentMissing", err)
	}
	if out != nil {
		t.Errorf("Splice returned non-nil buf on error: %q", out)
	}
}

// TestSpliceOrphanReplaceStillWorks verifies the READ/WRITE asymmetry
// only bites NEW inserts. Replacing an EXISTING orphan record goes
// through the exact-address match branch in Splice, which does not
// consult parentAddress and so is unaffected by strict-orphan. The
// existing "subsection.ta.quick-start" address is readable by
// TestOrphanH3UnderH1WithMissingH2; here we confirm it is also
// writable in-place.
func TestSpliceOrphanReplaceStillWorks(t *testing.T) {
	types := []record.DeclaredType{
		{Name: "title", Heading: 1},
		{Name: "section", Heading: 2},
		{Name: "subsection", Heading: 3},
	}
	b, err := NewBackend(types)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	src := []byte("# ta\n\n### Quick start\n\nRun mage install.\n")
	emitted := []byte("### Quick start\n\nRun `mage install -v` for verbose output.\n")
	out, err := b.Splice(src, "subsection.ta.quick-start", emitted)
	if err != nil {
		t.Fatalf("Splice replace on existing orphan: %v", err)
	}
	if !bytes.Contains(out, []byte("# ta\n")) {
		t.Error("H1 'ta' was lost on replace")
	}
	if !bytes.Contains(out, []byte("`mage install -v`")) {
		t.Error("new body missing from output")
	}
	if bytes.Contains(out, []byte("Run mage install.")) {
		t.Error("old body not replaced")
	}
}
