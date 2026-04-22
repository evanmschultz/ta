package md

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/record"
)

func TestNewBackendRejectsBadLevel(t *testing.T) {
	for _, lvl := range []int{-1, 0, 7, 99} {
		if _, err := NewBackend(lvl); err == nil {
			t.Errorf("NewBackend(%d): expected error", lvl)
		}
	}
	for lvl := 1; lvl <= 6; lvl++ {
		if _, err := NewBackend(lvl); err != nil {
			t.Errorf("NewBackend(%d): unexpected error %v", lvl, err)
		}
	}
}

func TestListWithScopePrefix(t *testing.T) {
	b, _ := NewBackend(2)
	src := []byte("# ta\n\n## Installation\n\nx\n\n## MCP client config\n\ny\n")
	got, err := b.List(src, "readme.section")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"readme.section.installation", "readme.section.mcp-client-config"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestListWithEmptyScopeReturnsSyntheticLocators(t *testing.T) {
	b, _ := NewBackend(2)
	src := []byte("# ta\n\n## Installation\n\nx\n")
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Synthetic form: H<level>.<slug> so the caller (resolver) can map it.
	want := []string{"H1.ta", "H2.installation"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestListFiltersByLevelMatchingScope(t *testing.T) {
	b, _ := NewBackend(1)
	src := []byte("# ta\n\n## Installation\n\n")
	// scope is <db>.<type> where type binds level 1 — only H1 matches.
	got, err := b.List(src, "readme.title")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"readme.title.ta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestFindReturnsByteRange(t *testing.T) {
	b, _ := NewBackend(2)
	src := []byte("# ta\n\n## Installation\n\nInstall from source.\n\n## Next\n\nmore\n")
	got, ok, err := b.Find(src, "readme.section.installation")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("section not found")
	}
	if got.Path != "readme.section.installation" {
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

func TestFindMissing(t *testing.T) {
	b, _ := NewBackend(2)
	src := []byte("# ta\n\n## Installation\n\nx\n")
	_, ok, err := b.Find(src, "readme.section.does-not-exist")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ok {
		t.Error("expected not found")
	}
}

func TestFindEmptyPathErrors(t *testing.T) {
	b, _ := NewBackend(2)
	if _, _, err := b.Find([]byte("# x\n"), ""); err == nil {
		t.Error("expected error on empty path")
	}
}

func TestEmitBodyOnly(t *testing.T) {
	b, _ := NewBackend(2)
	got, err := b.Emit("readme.section.installation", record.Record{
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
	b, _ := NewBackend(2)
	got, err := b.Emit("readme.section.todo", record.Record{})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.HasPrefix(got, []byte("## Todo\n")) {
		t.Errorf("got %q", got)
	}
}

func TestEmitEnsuresTrailingNewline(t *testing.T) {
	b, _ := NewBackend(3)
	got, err := b.Emit("x.y.foo", record.Record{"body": "no newline at end"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got[len(got)-1] != '\n' {
		t.Errorf("no trailing newline: %q", got)
	}
}

func TestEmitRejectsEmptySection(t *testing.T) {
	b, _ := NewBackend(2)
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
	b, _ := NewBackend(2)
	src := []byte("# ta\n\n## Installation\n\nold\n\n## Next\n\nkeep\n")
	emitted := []byte("## Installation\n\nnew body\n")
	out, err := b.Splice(src, "readme.section.installation", emitted)
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

func TestSpliceAppendWhenMissing(t *testing.T) {
	b, _ := NewBackend(2)
	src := []byte("# ta\n\n## A\n\nbody-a\n")
	emitted := []byte("## B\n\nbody-b\n")
	out, err := b.Splice(src, "readme.section.b", emitted)
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
	b, _ := NewBackend(1)
	emitted := []byte("# title\n\nbody\n")
	out, err := b.Splice(nil, "x.title.title", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if string(out) != "# title\n\nbody\n" {
		t.Errorf("got %q", out)
	}
}

func TestSpliceAppendEnsuresSeparator(t *testing.T) {
	b, _ := NewBackend(2)
	// Buffer ends without trailing newline.
	src := []byte("# ta\n\n## A\n\nbody-a")
	emitted := []byte("## B\n\nbody-b\n")
	out, err := b.Splice(src, "readme.section.b", emitted)
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
	b, _ := NewBackend(2)
	src := []byte("# ta\n\n## Installation\n\nInstall from source.\n\n## Next\n\nMore.\n")
	sec, ok, err := b.Find(src, "readme.section.installation")
	if err != nil || !ok {
		t.Fatalf("Find: ok=%v err=%v", ok, err)
	}
	orig := src[sec.Range[0]:sec.Range[1]]
	// Extract body (skip the heading line).
	firstNL := bytes.IndexByte(orig, '\n')
	body := string(orig[firstNL+1:])
	// Trim the leading blank line from body so Emit's own "\n\n" shim works.
	body = strings.TrimPrefix(body, "\n")

	emitted, err := b.Emit("readme.section.installation", record.Record{"body": body})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out, err := b.Splice(src, "readme.section.installation", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Equal(src, out) {
		t.Errorf("round-trip not byte-identical:\n--- src ---\n%s\n--- out ---\n%s", src, out)
	}
}

func TestSpliceEmptyReplacementRejected(t *testing.T) {
	b, _ := NewBackend(2)
	src := []byte("# x\n")
	if _, err := b.Splice(src, "x.y.z", nil); err == nil {
		t.Error("expected error on empty replacement")
	}
}

func TestSpliceEmptyPathRejected(t *testing.T) {
	b, _ := NewBackend(2)
	src := []byte("# x\n")
	if _, err := b.Splice(src, "", []byte("## X\n")); err == nil {
		t.Error("expected error on empty path")
	}
}

func TestCollisionPropagatesThroughListAndFind(t *testing.T) {
	b, _ := NewBackend(2)
	src := []byte("## Dup\n\nbody1\n## Dup\n\nbody2\n")
	if _, err := b.List(src, ""); !errors.Is(err, ErrSlugCollision) {
		t.Errorf("List: expected ErrSlugCollision, got %v", err)
	}
	if _, _, err := b.Find(src, "x.y.dup"); !errors.Is(err, ErrSlugCollision) {
		t.Errorf("Find: expected ErrSlugCollision, got %v", err)
	}
}
