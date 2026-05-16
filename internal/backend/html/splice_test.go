package html

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestHtmlSplice_Idempotent asserts that splicing the same selector with the
// same content twice produces byte-identical output. This is the surgical
// fold #1 invariant: splice must be deterministic and re-runnable.
func TestHtmlSplice_Idempotent(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head></head><body><p>hello</p></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	content := []byte(`<p>world</p>`)

	first, err := Splice(tree, buf, "p", content)
	if err != nil {
		t.Fatalf("Splice (first): %v", err)
	}
	// Re-parse the first result; the second splice must produce the same bytes.
	tree2, err := Parse(first)
	if err != nil {
		t.Fatalf("re-Parse: %v", err)
	}
	second, err := Splice(tree2, first, "p", content)
	if err != nil {
		t.Fatalf("Splice (second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("not idempotent:\nfirst : %q\nsecond: %q", first, second)
	}
}

// TestHtmlSplice_UpdatesNestedSelector asserts that splicing a selector
// matching a nested <p> inside a <div> only rewrites the <p>'s byte range
// and preserves the surrounding <div> bytes verbatim.
func TestHtmlSplice_UpdatesNestedSelector(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head></head><body><div class="outer"><p>old</p></div></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Splice(tree, buf, "div.outer > p", []byte(`<p>new content</p>`))
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	// The surrounding div bytes (open + close + class attribute) must survive.
	if !bytes.Contains(out, []byte(`<div class="outer">`)) {
		t.Errorf("outer <div> open tag not preserved: %q", out)
	}
	if !bytes.Contains(out, []byte(`</div>`)) {
		t.Errorf("outer <div> close tag not preserved: %q", out)
	}
	// The new <p> content must be present; the old must be gone.
	if !bytes.Contains(out, []byte(`<p>new content</p>`)) {
		t.Errorf("new <p> content missing: %q", out)
	}
	if bytes.Contains(out, []byte(`<p>old</p>`)) {
		t.Errorf("old <p> content still present: %q", out)
	}
}

// TestHtmlSplice_PreservesByteRangeOutsideSelector asserts the byte-identity
// invariant: every byte of buf that falls outside the matched element's
// full outer byte span ([open-tag-start, close-tag-end)) must appear
// verbatim in the output.
//
// The outer span is computed from the original buffer by locating "<p>"
// (open tag start) and "</p>" (after close tag), since parse.go's
// Tree.Offsets stores only per-token ranges; splice.outerRange composes
// them into the outer span at splice time.
func TestHtmlSplice_PreservesByteRangeOutsideSelector(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head><title>T</title></head><body><p>old</p><span>tail</span></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	openIdx := bytes.Index(buf, []byte("<p>"))
	if openIdx < 0 {
		t.Fatal("no <p> in buf")
	}
	closeEnd := bytes.Index(buf, []byte("</p>"))
	if closeEnd < 0 {
		t.Fatal("no </p> in buf")
	}
	closeEnd += len("</p>")

	content := []byte(`<p>X</p>`)
	out, err := Splice(tree, buf, "p", content)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	prefixWant := buf[:openIdx]
	if !bytes.HasPrefix(out, prefixWant) {
		t.Errorf("prefix mismatch:\nwant prefix: %q\ngot       : %q", prefixWant, out)
	}
	suffixWant := buf[closeEnd:]
	if !bytes.HasSuffix(out, suffixWant) {
		t.Errorf("suffix mismatch:\nwant suffix: %q\ngot       : %q", suffixWant, out)
	}
	// The replacement bytes appear at exactly the prefix boundary.
	wantAt := append(append([]byte{}, prefixWant...), content...)
	wantAt = append(wantAt, suffixWant...)
	if !bytes.Equal(out, wantAt) {
		t.Errorf("byte-identity outside outer span violated:\nwant: %q\ngot : %q", wantAt, out)
	}
}

// TestHtmlSplice_MalformedInputClarification verifies splice behaviour for
// well-formed and best-effort malformed inputs.
//
//   - well-formed: splice round-trips deterministically.
//   - malformed (unclosed-tags): parser auto-closes; splice still produces a
//     non-empty output containing the new content. Round-trip is lossy for
//     the missing close tag, which is the documented best-effort policy.
func TestHtmlSplice_MalformedInputClarification(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		selector    string
		content     string
		wantContain string
	}{
		{
			name:        "well-formed",
			input:       `<!DOCTYPE html><html><head></head><body><p>old</p></body></html>`,
			selector:    "p",
			content:     `<p>new</p>`,
			wantContain: `<p>new</p>`,
		},
		{
			name:        "unclosed-tag-best-effort",
			input:       `<div><p>old`,
			selector:    "p",
			content:     `<p>new</p>`,
			wantContain: `<p>new</p>`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			buf := []byte(tc.input)
			tree, err := Parse(buf)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			out, err := Splice(tree, buf, tc.selector, []byte(tc.content))
			if err != nil {
				t.Fatalf("Splice: %v", err)
			}
			if !bytes.Contains(out, []byte(tc.wantContain)) {
				t.Errorf("output missing %q: %q", tc.wantContain, out)
			}
		})
	}
}

// TestHtmlSplice_NoMatchReturnsErrSelectorNotFound asserts that a selector
// which compiles successfully but matches zero nodes returns
// ErrSelectorNotFound (surgical fold #3).
func TestHtmlSplice_NoMatchReturnsErrSelectorNotFound(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head></head><body><p>only paragraph</p></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Splice(tree, buf, "article.does-not-exist", []byte(`<article>x</article>`))
	if err == nil {
		t.Fatalf("expected error, got out=%q err=nil", out)
	}
	if !errors.Is(err, ErrSelectorNotFound) {
		t.Errorf("error %v is not ErrSelectorNotFound", err)
	}
	if out != nil {
		t.Errorf("zero-match should return nil buffer, got %q", out)
	}
}

// TestHtmlSplice_MultiMatchFirstWinsAmbiguousError asserts the first-match-
// wins policy (surgical fold #5) combined with the ambiguous-error wrap
// (surgical fold #4): for a selector matching 2+ nodes, Splice rewrites
// the first match's byte range AND returns an error satisfying
// errors.Is(err, ErrAmbiguousMatch).
func TestHtmlSplice_MultiMatchFirstWinsAmbiguousError(t *testing.T) {
	buf := []byte(`<!DOCTYPE html><html><head></head><body><p>first</p><p>second</p><p>third</p></body></html>`)
	tree, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	content := []byte(`<p>winner</p>`)
	out, err := Splice(tree, buf, "p", content)
	if err == nil {
		t.Fatalf("expected ambiguous error, got nil")
	}
	if !errors.Is(err, ErrAmbiguousMatch) {
		t.Errorf("error %v is not ErrAmbiguousMatch", err)
	}
	// Output must still be returned (first-match-wins).
	if out == nil {
		t.Fatal("first-match-wins requires a non-nil output buffer")
	}
	// The first <p>'s text was "first"; after splice with "winner", the new
	// content replaces the FIRST <p>, leaving "second" and "third" intact.
	if !bytes.Contains(out, []byte(`<p>winner</p>`)) {
		t.Errorf("winner content missing: %q", out)
	}
	if !bytes.Contains(out, []byte(`<p>second</p>`)) {
		t.Errorf("second <p> should be preserved: %q", out)
	}
	if !bytes.Contains(out, []byte(`<p>third</p>`)) {
		t.Errorf("third <p> should be preserved: %q", out)
	}
	// The original first <p>'s text "first" must be gone (it was overwritten).
	// Use a strict containment check: the literal `>first<` only appears
	// inside the original first paragraph.
	if strings.Contains(string(out), `>first<`) {
		t.Errorf("first <p> should have been overwritten, but `>first<` still present: %q", out)
	}
}
