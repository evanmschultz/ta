package html

import (
	"bytes"
	"errors"
	"testing"

	"github.com/evanmschultz/ta/internal/format"
)

// compile-time guarantee that HtmlBackend satisfies the format.Format
// contract. Mirrors var-blank-assignment style used by stdlib backends and
// the format package's own mockFormat.
var _ format.Format = (*HtmlBackend)(nil)

// newHtmlManifest builds a minimal *format.HtmlManifest with the supplied
// name→selector mapping. Tests don't need the FieldBindings / Description
// surface, so those stay nil/zero.
func newHtmlManifest(nameToSel map[string]string) *format.HtmlManifest {
	selToName := make(map[string]string, len(nameToSel))
	selectors := make([]string, 0, len(nameToSel))
	for name, sel := range nameToSel {
		selToName[sel] = name
		selectors = append(selectors, sel)
	}
	return &format.HtmlManifest{
		Selectors_:     selectors,
		SelectorToName: selToName,
		NameToSelector: nameToSel,
	}
}

// TestHtmlBackend_FormatInterfaceSatisfied complements the package-level
// compile-time assertion above by exercising every Format method through
// the interface, ensuring no method drift breaks the contract.
func TestHtmlBackend_FormatInterfaceSatisfied(t *testing.T) {
	var f format.Format = &HtmlBackend{}

	m := newHtmlManifest(map[string]string{
		"title": "title",
	})
	buf := []byte("<html><head><title>Hi</title></head><body>x</body></html>")

	blocks, err := f.Parse(buf, m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatalf("Parse returned 0 blocks; want >=1")
	}

	got, err := f.Find(buf, m, "title")
	if err != nil {
		t.Fatalf("Find(title): %v", err)
	}
	if !bytes.Contains(got, []byte("Hi")) {
		t.Errorf("Find(title) bytes = %q, want substring %q", got, "Hi")
	}

	spliced, err := f.Splice(buf, m, "title", []byte("<title>New</title>"))
	if err != nil {
		t.Fatalf("Splice(title): %v", err)
	}
	if !bytes.Contains(spliced, []byte("<title>New</title>")) {
		t.Errorf("Splice output missing new content: %q", spliced)
	}

	marshalled, err := f.Marshal(blocks, m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(marshalled) == 0 {
		t.Errorf("Marshal returned empty bytes for non-empty Blocks")
	}
}

// TestHtmlBackend_RegistersUnderHtmlKey verifies the package init() side
// effect: format.Dispatch("html") and format.Get("html") both resolve to a
// non-nil HtmlBackend instance.
func TestHtmlBackend_RegistersUnderHtmlKey(t *testing.T) {
	got, err := format.Get("html")
	if err != nil {
		t.Fatalf("format.Get(\"html\"): %v", err)
	}
	if got == nil {
		t.Fatal("format.Get(\"html\") returned nil")
	}
	if _, ok := got.(*HtmlBackend); !ok {
		t.Errorf("format.Get(\"html\") returned %T, want *HtmlBackend", got)
	}

	dispatched, err := format.Dispatch("html")
	if err != nil {
		t.Fatalf("format.Dispatch(\"html\"): %v", err)
	}
	if dispatched != got {
		t.Errorf("Dispatch/Get returned different impls: %v vs %v", dispatched, got)
	}
}

// TestHtmlBackend_ManifestSelectorMapping verifies name→selector resolution
// drives both Find and Splice. The manifest declares names that do NOT
// match the underlying CSS selectors so a happy-path miss would surface as
// an ErrBlockNotFound rather than an accidental literal-match.
func TestHtmlBackend_ManifestSelectorMapping(t *testing.T) {
	b := &HtmlBackend{}

	m := newHtmlManifest(map[string]string{
		"page_title":     "title",
		"page_paragraph": "p",
	})
	buf := []byte("<html><head><title>Doc Title</title></head><body><p>Para text</p></body></html>")

	titleBytes, err := b.Find(buf, m, "page_title")
	if err != nil {
		t.Fatalf("Find(page_title): %v", err)
	}
	if !bytes.Equal(titleBytes, []byte("<title>Doc Title</title>")) {
		t.Errorf("Find(page_title) = %q, want %q", titleBytes, "<title>Doc Title</title>")
	}

	paraBytes, err := b.Find(buf, m, "page_paragraph")
	if err != nil {
		t.Fatalf("Find(page_paragraph): %v", err)
	}
	if !bytes.Equal(paraBytes, []byte("<p>Para text</p>")) {
		t.Errorf("Find(page_paragraph) = %q, want %q", paraBytes, "<p>Para text</p>")
	}

	// Name unknown to manifest → ErrBlockNotFound.
	_, err = b.Find(buf, m, "no_such_name")
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Find(no_such_name) error = %v, want errors.Is(_, ErrBlockNotFound)", err)
	}

	// Splice via the same name-mapping path.
	out, err := b.Splice(buf, m, "page_paragraph", []byte("<p>Replaced</p>"))
	if err != nil {
		t.Fatalf("Splice(page_paragraph): %v", err)
	}
	if !bytes.Contains(out, []byte("<p>Replaced</p>")) {
		t.Errorf("Splice output missing replacement: %q", out)
	}
	if bytes.Contains(out, []byte("Para text")) {
		t.Errorf("Splice output still contains original text: %q", out)
	}

	// Splice with unknown name → ErrBlockNotFound.
	_, err = b.Splice(buf, m, "no_such_name", []byte("x"))
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Splice(no_such_name) error = %v, want errors.Is(_, ErrBlockNotFound)", err)
	}

	// Splice whose resolved selector matches zero nodes → ErrBlockNotFound
	// (the underlying ErrSelectorNotFound is translated to the format-level
	// not-found sentinel so callers code against a single contract).
	mNoMatch := newHtmlManifest(map[string]string{"footer": "footer"})
	_, err = b.Splice(buf, mNoMatch, "footer", []byte("x"))
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Splice(footer) error = %v, want errors.Is(_, ErrBlockNotFound)", err)
	}
}

// TestHtmlBackend_BlockSpansByteIdenticalToInputBuf verifies the load-bearing
// invariant the splice layer depends on: each parsed Block's Bytes equals
// buf[Start:End] byte-for-byte. This is the per-block byte-identity gate;
// the Marshal output is a separate concern tested by
// TestHtmlBackend_MarshalConcatenatesBlocksWithNewlineSeparator.
func TestHtmlBackend_BlockSpansByteIdenticalToInputBuf(t *testing.T) {
	b := &HtmlBackend{}
	m := newHtmlManifest(map[string]string{
		"a": "section.a",
		"b": "section.b",
	})
	buf := []byte(`<section class="a">Alpha</section><section class="b">Beta</section>`)

	blocks, err := b.Parse(buf, m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("Parse returned %d blocks, want 2", len(blocks))
	}
	if blocks[0].Name != "a" || blocks[1].Name != "b" {
		t.Errorf("block order = [%q, %q], want [a, b]", blocks[0].Name, blocks[1].Name)
	}
	for _, blk := range blocks {
		if blk.Start < 0 || blk.End > len(buf) || blk.End < blk.Start {
			t.Errorf("block %q invalid range [%d,%d) over buf len %d", blk.Name, blk.Start, blk.End, len(buf))
			continue
		}
		if !bytes.Equal(blk.Bytes, buf[blk.Start:blk.End]) {
			t.Errorf("block %q Bytes %q != buf[%d:%d] %q", blk.Name, blk.Bytes, blk.Start, blk.End, buf[blk.Start:blk.End])
		}
	}
}

// TestHtmlBackend_MarshalConcatenatesBlocksWithNewlineSeparator pins the
// Marshal contract: best-effort concatenation of Block.Bytes joined by a
// single newline. Marshal is NOT a byte-identical round-trip of the input
// buf when the input has no separators — the newline is INJECTED. Callers
// needing arbitrary HTML synthesis must use a template engine.
func TestHtmlBackend_MarshalConcatenatesBlocksWithNewlineSeparator(t *testing.T) {
	b := &HtmlBackend{}
	m := newHtmlManifest(map[string]string{
		"a": "section.a",
		"b": "section.b",
	})
	buf := []byte(`<section class="a">Alpha</section><section class="b">Beta</section>`)

	blocks, err := b.Parse(buf, m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := b.Marshal(blocks, m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Marshal injects '\n' between blocks even when input had no separator.
	// This test pins that contract; do NOT change the want value to match
	// buf without first updating the Marshal contract + documentation.
	want := []byte(`<section class="a">Alpha</section>` + "\n" + `<section class="b">Beta</section>`)
	if !bytes.Equal(out, want) {
		t.Errorf("Marshal output mismatch\n got: %q\nwant: %q", out, want)
	}
}
