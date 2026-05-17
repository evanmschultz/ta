package txt

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
	"unsafe"

	"github.com/evanmschultz/ta/internal/format"
)

// compile-time check: package init registers Backend, so format.Get("txt")
// must resolve to a non-nil Format. The test exists so a future refactor
// that drops the init() registration trips a clear failure.
func TestTxtBackend_Registered(t *testing.T) {
	f, err := format.Get("txt")
	if err != nil {
		t.Fatalf(`format.Get("txt"): %v`, err)
	}
	if f == nil {
		t.Fatal(`format.Get("txt") returned nil Format`)
	}
	if _, ok := f.(*Backend); !ok {
		t.Errorf(`format.Get("txt") = %T, want *Backend`, f)
	}
}

// newTxtManifest is a tiny test helper: build a *format.TxtManifest with
// the given (block-name → pattern) map, pre-compiling every pattern. The
// Selectors_ list is sorted by block name for reproducibility (mirrors
// the loader's normalizeSelectors behavior).
func newTxtManifest(t *testing.T, patterns map[string]string) *format.TxtManifest {
	t.Helper()
	compiled := make(map[string]*regexp.Regexp, len(patterns))
	nameToSel := make(map[string]string, len(patterns))
	selToName := make(map[string]string, len(patterns))
	var names []string
	for name, pat := range patterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			t.Fatalf("regexp.Compile(%q) for block %q: %v", pat, name, err)
		}
		compiled[name] = re
		nameToSel[name] = pat
		selToName[pat] = name
		names = append(names, name)
	}
	// Sort names so Selectors_ is reproducible. Matches the loader contract.
	sortStrings(names)
	sels := make([]string, 0, len(names))
	for _, n := range names {
		sels = append(sels, nameToSel[n])
	}
	return &format.TxtManifest{
		Selectors_:     sels,
		SelectorToName: selToName,
		NameToSelector: nameToSel,
		Compiled:       compiled,
	}
}

// sortStrings is an in-place ascending sort for short string slices. Avoids
// dragging sort into the test file imports for a single 3-element sort.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// TestTxtBackend_RoundTripByteIdentical pins CE-6 no-edit path: Parse →
// Marshal returns bytes byte-EQUAL to the original input when the
// manifest's regex selectors cover the entire buffer with no gaps and no
// Block.Bytes has been edited between Parse and Marshal.
//
// Fixture: three lines each fully covered by one (?m)-anchored pattern.
func TestTxtBackend_RoundTripByteIdentical(t *testing.T) {
	buf := []byte("HEADER: alpha\n## section one\nFOOTER: omega\n")
	m := newTxtManifest(t, map[string]string{
		"header":  `(?m)^HEADER:.*\n`,
		"section": `(?m)^## .*\n`,
		"footer":  `(?m)^FOOTER:.*\n`,
	})

	b := &Backend{}
	blocks, err := b.Parse(buf, m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("Parse returned %d blocks, want 3", len(blocks))
	}
	out, err := b.Marshal(blocks, m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, buf) {
		t.Errorf("round-trip not byte-identical:\n  got:  %q\n  want: %q", out, buf)
	}
}

// TestTxtBackend_RoundTripWithEditPreservesUnedited pins CE-6 edit path:
// when Splice rewrites one block, the edited region's bytes differ but
// every byte outside that region stays byte-identical to the original buf.
func TestTxtBackend_RoundTripWithEditPreservesUnedited(t *testing.T) {
	buf := []byte("HEADER: alpha\n## section one\nFOOTER: omega\n")
	m := newTxtManifest(t, map[string]string{
		"header":  `(?m)^HEADER:.*\n`,
		"section": `(?m)^## .*\n`,
		"footer":  `(?m)^FOOTER:.*\n`,
	})

	b := &Backend{}
	// Locate the section block to capture its byte range pre-splice.
	pre, _, err := FindBlock(buf, "section", m.Compiled["section"])
	if err != nil {
		t.Fatalf("FindBlock(section) on original buf: %v", err)
	}
	preStart, preEnd := pre.Start, pre.End

	newContent := []byte("## section EDITED\n")
	out, err := b.Splice(buf, m, "section", newContent)
	if err != nil {
		t.Fatalf("Splice(section): %v", err)
	}

	// Bytes before the edited region must be byte-identical.
	if !bytes.Equal(out[:preStart], buf[:preStart]) {
		t.Errorf("bytes before edited region changed:\n  got:  %q\n  want: %q", out[:preStart], buf[:preStart])
	}
	// Bytes after the edited region must be byte-identical. The post-edit
	// region starts at preStart+len(newContent) in `out`; the original
	// equivalent starts at preEnd in `buf`. Both spans must match.
	if !bytes.Equal(out[preStart+len(newContent):], buf[preEnd:]) {
		t.Errorf("bytes after edited region changed:\n  got:  %q\n  want: %q", out[preStart+len(newContent):], buf[preEnd:])
	}
	// The edited region itself must equal newContent.
	if !bytes.Equal(out[preStart:preStart+len(newContent)], newContent) {
		t.Errorf("edited region != newContent:\n  got:  %q\n  want: %q", out[preStart:preStart+len(newContent)], newContent)
	}
	// The original buf must NOT have been mutated (Splice returns a new
	// slice; the caller's input is read-only).
	if !bytes.Equal(buf, []byte("HEADER: alpha\n## section one\nFOOTER: omega\n")) {
		t.Errorf("Splice mutated caller buf: %q", buf)
	}
}

// TestTxtBackend_ManifestRegexSelectorMapping pins that Find resolves
// block-name → compiled regex via the manifest and returns the matched
// byte range. Uses a (?m)-anchored pattern + multi-line buffer.
func TestTxtBackend_ManifestRegexSelectorMapping(t *testing.T) {
	buf := []byte("noise\nHEADER: target\nmore noise\n")
	m := newTxtManifest(t, map[string]string{
		"header": `(?m)^HEADER:.*$`,
	})

	b := &Backend{}
	got, err := b.Find(buf, m, "header")
	if err != nil {
		t.Fatalf("Find(header): %v", err)
	}
	if !bytes.Equal(got, []byte("HEADER: target")) {
		t.Errorf("Find returned %q, want %q", got, "HEADER: target")
	}
}

// TestTxtBackend_FindZeroMatchReturnsErrBlockNotFound pins the
// substrate sentinel: a regex matching nothing surfaces as
// format.ErrBlockNotFound through the backend (wrapped, errors.Is-detectable).
func TestTxtBackend_FindZeroMatchReturnsErrBlockNotFound(t *testing.T) {
	buf := []byte("nothing relevant here\n")
	m := newTxtManifest(t, map[string]string{
		"summary": `(?m)^summary:\s+.+$`,
	})

	b := &Backend{}
	got, err := b.Find(buf, m, "summary")
	if err == nil {
		t.Fatalf("Find returned nil error and bytes %q; want ErrBlockNotFound", got)
	}
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("errors.Is(err, format.ErrBlockNotFound) = false; err = %v", err)
	}
	if errors.Is(err, format.ErrAmbiguousMatch) {
		t.Errorf("errors.Is(err, format.ErrAmbiguousMatch) = true; sibling sentinel must not match")
	}
}

// TestTxtBackend_FindUnknownNameReturnsErrBlockNotFound pins the
// name-not-in-manifest path: even when the buffer would match SOME
// pattern, a name the manifest doesn't carry must surface as
// format.ErrBlockNotFound.
func TestTxtBackend_FindUnknownNameReturnsErrBlockNotFound(t *testing.T) {
	buf := []byte("HEADER: present\n")
	m := newTxtManifest(t, map[string]string{
		"header": `(?m)^HEADER:.*$`,
	})

	b := &Backend{}
	_, err := b.Find(buf, m, "missing_block")
	if err == nil {
		t.Fatal("Find(unknown name) returned nil error; want ErrBlockNotFound")
	}
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("errors.Is(err, format.ErrBlockNotFound) = false; err = %v", err)
	}
}

// TestTxtBackend_FindAmbiguousMatchReturnsErrAmbiguous pins CE-1 multi-
// match policy at the backend layer: a pattern matching multiple
// candidates surfaces format.ErrAmbiguousMatch (NOT first-match-wins).
func TestTxtBackend_FindAmbiguousMatchReturnsErrAmbiguous(t *testing.T) {
	buf := []byte("HEADER: one\nHEADER: two\nHEADER: three\n")
	m := newTxtManifest(t, map[string]string{
		"header": `(?m)^HEADER:.*$`,
	})

	b := &Backend{}
	_, err := b.Find(buf, m, "header")
	if err == nil {
		t.Fatal("Find on multi-match buf returned nil error; want ErrAmbiguousMatch")
	}
	if !errors.Is(err, format.ErrAmbiguousMatch) {
		t.Errorf("errors.Is(err, format.ErrAmbiguousMatch) = false; err = %v", err)
	}
	if errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("errors.Is(err, format.ErrBlockNotFound) = true; sibling sentinel must not match")
	}
}

// TestTxtBackend_SpliceIdempotent pins CE-3: a second Splice with the
// same content produces a byte-identical result to the first Splice
// (Splice has no internal state; it always rewrites the matched range
// to the supplied content).
func TestTxtBackend_SpliceIdempotent(t *testing.T) {
	buf := []byte("HEADER: alpha\nbody bytes\nFOOTER: omega\n")
	m := newTxtManifest(t, map[string]string{
		"header": `(?m)^HEADER:.*\n`,
	})

	b := &Backend{}
	first, err := b.Splice(buf, m, "header", []byte("HEADER: beta\n"))
	if err != nil {
		t.Fatalf("first Splice: %v", err)
	}
	second, err := b.Splice(first, m, "header", []byte("HEADER: beta\n"))
	if err != nil {
		t.Fatalf("second Splice: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("second Splice not byte-identical to first:\n  first:  %q\n  second: %q", first, second)
	}
}

// TestTxtBackend_SpliceUnknownNameReturnsErrBlockNotFound pins the
// not-found sentinel for the Splice path when the block name is not in
// the manifest at all.
func TestTxtBackend_SpliceUnknownNameReturnsErrBlockNotFound(t *testing.T) {
	buf := []byte("HEADER: alpha\n")
	m := newTxtManifest(t, map[string]string{
		"header": `(?m)^HEADER:.*\n`,
	})
	b := &Backend{}
	_, err := b.Splice(buf, m, "no_such_block", []byte("x"))
	if err == nil {
		t.Fatal("Splice(unknown name) returned nil error; want ErrBlockNotFound")
	}
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("errors.Is(err, format.ErrBlockNotFound) = false; err = %v", err)
	}
}

// TestTxtBackend_SubSliceBytesPreservedThroughSplice pins routed concern 3:
// after Parse, each Block.Bytes is a zero-copy sub-slice of the input
// buffer — modifying one byte of buf at Block.Start reflects in
// Block.Bytes[0] because they share the underlying array. Splice does NOT
// mutate the caller's buf; the returned slice is freshly allocated.
//
// Verification strategy: compare unsafe.SliceData pointers — Block.Bytes
// must share the underlying array with buf (zero-copy from FindBlock).
// Splice's output must point to a DIFFERENT array (defensive new
// allocation).
func TestTxtBackend_SubSliceBytesPreservedThroughSplice(t *testing.T) {
	buf := []byte("HEADER: alpha\nFOOTER: omega\n")
	m := newTxtManifest(t, map[string]string{
		"header": `(?m)^HEADER:.*\n`,
	})

	b := &Backend{}
	blocks, err := b.Parse(buf, m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("Parse returned %d blocks, want 1", len(blocks))
	}
	// Zero-copy: Block.Bytes underlying array == buf underlying array.
	if unsafe.SliceData(blocks[0].Bytes) != unsafe.SliceData(buf) {
		t.Errorf("Block.Bytes does not share buf underlying array; zero-copy contract broken")
	}
	// Splice must produce a NEW slice (not aliased into buf).
	out, err := b.Splice(buf, m, "header", []byte("HEADER: beta\n"))
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if unsafe.SliceData(out) == unsafe.SliceData(buf) {
		t.Errorf("Splice output aliases caller buf; defensive copy contract broken")
	}
	// And the caller's buf must be unchanged.
	if !bytes.Equal(buf, []byte("HEADER: alpha\nFOOTER: omega\n")) {
		t.Errorf("caller buf mutated by Splice: %q", buf)
	}
}

// TestTxtBackend_ParseSkipsZeroWidthSelector pins that a manifest whose
// regex collapses to a zero-width single match (e.g. anchor-only `^`)
// silently contributes zero blocks to Parse output — FindBlock returns
// ErrBlockNotFound and Parse treats it as a non-match. This mirrors the
// CE-collapse contract from L3-D4-D1.
func TestTxtBackend_ParseSkipsZeroWidthSelector(t *testing.T) {
	buf := []byte("ab\ncd\n")
	m := newTxtManifest(t, map[string]string{
		// Pattern with literal-bound matches that are zero-width collapses
		// to ErrBlockNotFound when there's only one such anchor position.
		// Use `^$` on an empty buffer to exercise: but that's not this
		// fixture. Use `^` which produces zero-width matches; FindBlock
		// returns ambiguous if >1 anchor — so use a single-line buf with
		// `^` against no newline: matches the single position 0, width 0.
		"anchor_only": `^`,
	})
	// Build a buf where `^` matches exactly ONCE (a buffer with no newline
	// produces a single anchor position at offset 0, width 0).
	singleAnchorBuf := []byte("noNewline")

	b := &Backend{}
	blocks, err := b.Parse(singleAnchorBuf, m)
	if err != nil {
		t.Fatalf("Parse with zero-width-collapse pattern: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("Parse returned %d blocks for zero-width selector; want 0 (FindBlock collapses to ErrBlockNotFound)", len(blocks))
	}
	// Silence buf-unused on the multi-line example used for fixture prose.
	_ = buf
}

// TestTxtBackend_ParsePropagatesAmbiguousFromOneSelector pins that an
// ambiguous-match for ONE manifest entry surfaces as a Parse error rather
// than being silently skipped — Parse must not mask manifest-author bugs
// just because OTHER selectors matched cleanly.
func TestTxtBackend_ParsePropagatesAmbiguousFromOneSelector(t *testing.T) {
	buf := []byte("HEADER: one\nHEADER: two\nLINE: solo\n")
	m := newTxtManifest(t, map[string]string{
		"header":   `(?m)^HEADER:.*$`, // matches 2 lines — ambiguous
		"solitary": `(?m)^LINE:.*$`,   // matches 1 line — fine
	})

	b := &Backend{}
	_, err := b.Parse(buf, m)
	if err == nil {
		t.Fatal("Parse returned nil error despite ambiguous selector; want ErrAmbiguousMatch")
	}
	if !errors.Is(err, format.ErrAmbiguousMatch) {
		t.Errorf("errors.Is(err, format.ErrAmbiguousMatch) = false; err = %v", err)
	}
}

// TestTxtBackend_MarshalEmptyBlocks pins the defensive empty-input
// behavior: Marshal([]) returns an empty (non-nil) slice, never nil.
func TestTxtBackend_MarshalEmptyBlocks(t *testing.T) {
	b := &Backend{}
	got, err := b.Marshal(nil, nil)
	if err != nil {
		t.Fatalf("Marshal(nil): %v", err)
	}
	if got == nil {
		t.Error("Marshal(nil) returned nil; want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("Marshal(nil) = %q, want empty", got)
	}
}
