package md_explicit

import (
	"bytes"
	"errors"
	"testing"

	"github.com/evanmschultz/ta/internal/backend/md"
	"github.com/evanmschultz/ta/internal/format"
	"github.com/evanmschultz/ta/internal/record"
)

// newManifest builds a *format.MdManifest from a {name: selector} map
// the same way internal/format/manifest.go's normalizeSelectors does:
// SelectorToName and NameToSelector inverses, Selectors_ sorted by
// block-name alphabetic order. Tests use this to avoid reaching into
// the format package's private build helpers.
func newManifest(nameToSel map[string]string) *format.MdManifest {
	selToName := make(map[string]string, len(nameToSel))
	names := make([]string, 0, len(nameToSel))
	for name, sel := range nameToSel {
		selToName[sel] = name
		names = append(names, name)
	}
	// Sort names so Selectors() is reproducible across runs.
	// (Mirrors normalizeSelectors's alphabetic sort.)
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	sels := make([]string, 0, len(names))
	for _, n := range names {
		sels = append(sels, nameToSel[n])
	}
	return &format.MdManifest{
		Selectors_:     sels,
		SelectorToName: selToName,
		NameToSelector: nameToSel,
	}
}

// TestMdExplicitBackend_RegisteredUnderMdKey pins amendment 2: the
// backend is registered in the format registry under "md" (NOT
// "md_explicit"), and format.Get("md") returns this package's *Backend.
func TestMdExplicitBackend_RegisteredUnderMdKey(t *testing.T) {
	f, err := format.Get("md")
	if err != nil {
		t.Fatalf("format.Get(%q): unexpected err %v", "md", err)
	}
	if _, ok := f.(*Backend); !ok {
		t.Fatalf("format.Get(%q) returned %T, want *md_explicit.Backend", "md", f)
	}
	if _, err := format.Get("md_explicit"); err == nil {
		t.Fatalf("format.Get(%q) returned nil error; want unregistered-key error", "md_explicit")
	}
}

// TestMdExplicitBackend_RoundTripByteIdentical: Parse → Marshal is
// byte-equal to the input when the input is fully tiled by declared
// blocks (every byte sits inside some manifest-matched heading
// subtree). This pins the contract documented on Marshal — lossless
// round-trip when input is entirely named-block content.
func TestMdExplicitBackend_RoundTripByteIdentical(t *testing.T) {
	// Buffer where every byte is inside a depth-1 heading subtree, and
	// both depth-1 headings are declared in the manifest. Adjacent
	// sibling spans tile the entire buffer end-to-end.
	buf := []byte("# Alpha\nalpha-body\n# Beta\nbeta-body\n")
	m := newManifest(map[string]string{
		"alpha": "Alpha",
		"beta":  "Beta",
	})

	be := &Backend{}
	blocks, err := be.Parse(buf, m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("Parse: want 2 blocks, got %d (%+v)", len(blocks), blocks)
	}
	if blocks[0].Start != 0 {
		t.Errorf("blocks[0].Start = %d, want 0", blocks[0].Start)
	}
	if blocks[1].End != len(buf) {
		t.Errorf("blocks[1].End = %d, want %d", blocks[1].End, len(buf))
	}

	out, err := be.Marshal(blocks, m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, buf) {
		t.Fatalf("round-trip not byte-identical:\nwant %q\ngot  %q", buf, out)
	}
}

// TestMdExplicitBackend_ManifestHeadingPathMapping pins the manifest
// name → heading-path translation: a multi-segment selector like
// "Intro > Setup" registered under name "setup" resolves via Find to
// the matching block bytes.
func TestMdExplicitBackend_ManifestHeadingPathMapping(t *testing.T) {
	buf := []byte("# Intro\nintro-body\n## Setup\nsetup-body\n## Other\nother-body\n")
	m := newManifest(map[string]string{
		"setup": "Intro > Setup",
	})

	be := &Backend{}
	got, err := be.Find(buf, m, "setup")
	if err != nil {
		t.Fatalf("Find(setup): unexpected err %v", err)
	}
	wantStart := []byte("## Setup\n")
	if !bytes.HasPrefix(got, wantStart) {
		t.Errorf("Find(setup) body does not start with %q: got %q", wantStart, got)
	}
	if !bytes.Contains(got, []byte("setup-body")) {
		t.Errorf("Find(setup) body missing %q: got %q", "setup-body", got)
	}
	// Block ends BEFORE the next sibling-or-shallower heading.
	if bytes.Contains(got, []byte("## Other")) {
		t.Errorf("Find(setup) body should not include ## Other heading: got %q", got)
	}

	// Parse via the same manifest also yields exactly one block.
	blocks, err := be.Parse(buf, m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("Parse: want 1 block, got %d", len(blocks))
	}
	if blocks[0].Name != "setup" {
		t.Errorf("block name = %q, want %q", blocks[0].Name, "setup")
	}
}

// TestMdExplicitBackend_ZeroSideEffectOnExistingMdBackend implements
// the concrete 3-part check from drop_004 L3-D3 amendment 7:
//
//  1. Parse a fixture markdown buffer through both internal/backend/md/
//     and internal/backend/md_explicit/ in the same binary.
//  2. Verify md/'s parse output is byte-identical with vs without
//     md_explicit/ active (i.e. consecutive md.Backend.List calls
//     produce the same output — no shared mutable global mutated by
//     md_explicit/ init).
//  3. Verify md/ is NOT registered under format.Register("md") at any
//     point — format.Get("md") must return *md_explicit.Backend, not
//     anything from the md package.
func TestMdExplicitBackend_ZeroSideEffectOnExistingMdBackend(t *testing.T) {
	// Part 1 + 2: md.Backend on a fixture.
	mdBackend, err := md.NewBackend([]record.DeclaredType{
		{Name: "title", Heading: 1},
		{Name: "section", Heading: 2},
	})
	if err != nil {
		t.Fatalf("md.NewBackend: %v", err)
	}
	fixture := []byte("# ta\n\n## Installation\n\nbody\n\n## MCP\n\nm\n")

	got1, err := mdBackend.List(fixture, "")
	if err != nil {
		t.Fatalf("md.List call 1: %v", err)
	}

	// Run md_explicit substrate paths in between to expose any hidden
	// shared-state mutation. Both Parse and a Find through the
	// md_explicit Backend exercise WalkHeadings + FindByPath internals.
	be := &Backend{}
	mExplicit := newManifest(map[string]string{
		"alpha": "ta",
	})
	if _, err := be.Parse(fixture, mExplicit); err != nil {
		t.Fatalf("md_explicit.Parse: %v", err)
	}
	if _, err := be.Find(fixture, mExplicit, "alpha"); err != nil {
		t.Fatalf("md_explicit.Find: %v", err)
	}

	got2, err := mdBackend.List(fixture, "")
	if err != nil {
		t.Fatalf("md.List call 2: %v", err)
	}
	if len(got1) != len(got2) {
		t.Fatalf("md.List length changed across md_explicit calls: %d vs %d", len(got1), len(got2))
	}
	for i := range got1 {
		if got1[i] != got2[i] {
			t.Errorf("md.List[%d]: changed across md_explicit calls: %q vs %q", i, got1[i], got2[i])
		}
	}

	// Part 3: format.Get("md") returns the md_explicit Backend. The md
	// package must not be registered under any format key.
	got, err := format.Get("md")
	if err != nil {
		t.Fatalf("format.Get(%q): %v", "md", err)
	}
	if _, ok := got.(*Backend); !ok {
		t.Errorf("format.Get(%q) returned %T, want *md_explicit.Backend (md package must not register)", "md", got)
	}
}

// TestMdExplicitBackend_SpliceIdempotent: a second Splice with the same
// content as the first produces a byte-identical buffer. This pins the
// invariant that Splice's outside-range preservation is stable across
// repeat invocations.
func TestMdExplicitBackend_SpliceIdempotent(t *testing.T) {
	buf := []byte("# Top\ntop-body\n## Setup\nold-body\n## After\nafter-body\n")
	m := newManifest(map[string]string{
		"setup": "Top > Setup",
	})
	be := &Backend{}

	replacement := []byte("## Setup\nnew-body\n")
	r1, err := be.Splice(buf, m, "setup", replacement)
	if err != nil {
		t.Fatalf("Splice 1: %v", err)
	}
	r2, err := be.Splice(r1, m, "setup", replacement)
	if err != nil {
		t.Fatalf("Splice 2: %v", err)
	}
	if !bytes.Equal(r1, r2) {
		t.Fatalf("Splice not idempotent:\nr1 = %q\nr2 = %q", r1, r2)
	}

	// Sanity: r1 differs from buf (the splice actually replaced bytes).
	if bytes.Equal(r1, buf) {
		t.Fatalf("Splice did not modify buffer; test setup wrong")
	}
	// Outside the range stayed verbatim: prefix "# Top\ntop-body\n" and
	// suffix "## After\nafter-body\n" remain.
	if !bytes.HasPrefix(r1, []byte("# Top\ntop-body\n")) {
		t.Errorf("Splice removed pre-block bytes: %q", r1)
	}
	if !bytes.HasSuffix(r1, []byte("## After\nafter-body\n")) {
		t.Errorf("Splice removed post-block bytes: %q", r1)
	}
}

// TestMdExplicitBackend_DuplicatePathMultiMatch pins routed concern 2:
// when a heading-path selector matches more than one heading subtree,
// Find returns the first-match bytes PLUS a wrapped
// format.ErrAmbiguousMatch sentinel. Symmetry with the html backend's
// pattern (NOT txt — txt returns nil-on-ambiguous; md_explicit follows
// html because heading-path first match is stable and useful).
func TestMdExplicitBackend_DuplicatePathMultiMatch(t *testing.T) {
	// Two depth-1 "A" headings each with a depth-2 "B" child.
	// Selector "A > B" matches both.
	buf := []byte("# A\n## B\nbody1\n# A\n## B\nbody2\n")
	m := newManifest(map[string]string{
		"target": "A > B",
	})
	be := &Backend{}

	got, err := be.Find(buf, m, "target")
	if !errors.Is(err, format.ErrAmbiguousMatch) {
		t.Fatalf("Find(target) on duplicate path: want ErrAmbiguousMatch, got %v", err)
	}
	// Non-nil first-match body returned alongside the sentinel.
	if !bytes.Contains(got, []byte("body1")) {
		t.Errorf("Find(target) first-match body should contain %q, got %q", "body1", got)
	}
	if bytes.Contains(got, []byte("body2")) {
		t.Errorf("Find(target) first-match body must NOT include second match: got %q", got)
	}

	// Splice same policy: returns non-nil out + wrapped ErrAmbiguousMatch.
	out, err := be.Splice(buf, m, "target", []byte("## B\nreplaced\n"))
	if !errors.Is(err, format.ErrAmbiguousMatch) {
		t.Fatalf("Splice(target) on duplicate path: want ErrAmbiguousMatch, got %v", err)
	}
	if !bytes.Contains(out, []byte("replaced")) {
		t.Errorf("Splice(target) output missing replacement: %q", out)
	}
	if !bytes.Contains(out, []byte("body2")) {
		t.Errorf("Splice(target) must keep the second match intact (first-match-wins): %q", out)
	}
}

// TestMdExplicitBackend_ParseDuplicatePathReturnsFirstMatchPlusAmbiguousErr
// pins the now-symmetric multi-match contract between Parse and
// Find/Splice. When one or more manifest selectors resolve to multiple
// heading subtrees, Parse returns the FIRST match per selector AND a
// wrapped format.ErrAmbiguousMatch alongside the populated Blocks
// slice — same shape Find/Splice use. Callers consume blocks and
// errors.Is-detect the ambiguity to decide whether to accept first-match
// or tighten manifest selectors.
func TestMdExplicitBackend_ParseDuplicatePathReturnsFirstMatchPlusAmbiguousErr(t *testing.T) {
	buf := []byte("# A\n## B\nbody1\n# A\n## B\nbody2\n")
	m := newManifest(map[string]string{
		"target": "A > B",
	})
	be := &Backend{}

	blocks, err := be.Parse(buf, m)
	if err == nil {
		t.Fatal("Parse on duplicate-path manifest: expected wrapped ErrAmbiguousMatch, got nil")
	}
	if !errors.Is(err, format.ErrAmbiguousMatch) {
		t.Errorf("Parse error %v: want errors.Is(err, format.ErrAmbiguousMatch) = true", err)
	}
	if got, want := len(blocks), 1; got != want {
		t.Fatalf("Parse blocks count = %d, want %d (first-match contract preserved alongside ambiguity error)", got, want)
	}
	if !bytes.Contains(blocks[0].Bytes, []byte("body1")) {
		t.Errorf("Parse first-match block should contain %q, got %q", "body1", blocks[0].Bytes)
	}
	if bytes.Contains(blocks[0].Bytes, []byte("body2")) {
		t.Errorf("Parse first-match block must NOT include second match: got %q", blocks[0].Bytes)
	}
}

// TestMdExplicitBackend_FindNotFound pins the ErrBlockNotFound surface:
// unknown manifest name OR known name whose selector matches no
// heading both produce format.ErrBlockNotFound (wrapped). Symmetric
// with the html backend's not-found contract.
func TestMdExplicitBackend_FindNotFound(t *testing.T) {
	buf := []byte("# A\n## B\nbody\n")
	m := newManifest(map[string]string{
		"known": "A > B",
	})
	be := &Backend{}

	// Unknown name.
	_, err := be.Find(buf, m, "unknown")
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Find(unknown): want ErrBlockNotFound, got %v", err)
	}

	// Nil manifest.
	_, err = be.Find(buf, nil, "anything")
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Find(nil-manifest): want ErrBlockNotFound, got %v", err)
	}

	// Known name but no matching heading.
	mMiss := newManifest(map[string]string{
		"missing": "Nope > NotHere",
	})
	_, err = be.Find(buf, mMiss, "missing")
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Find(known-but-no-match): want ErrBlockNotFound, got %v", err)
	}
}

// TestMdExplicitBackend_SpliceNotFound covers the Splice ErrBlockNotFound
// surface symmetrically with Find. Splice is the write side of the
// same not-found contract.
func TestMdExplicitBackend_SpliceNotFound(t *testing.T) {
	buf := []byte("# A\n## B\nbody\n")
	be := &Backend{}

	// Nil manifest.
	_, err := be.Splice(buf, nil, "anything", []byte("x"))
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Splice(nil-manifest): want ErrBlockNotFound, got %v", err)
	}

	// Known manifest, unknown name.
	m := newManifest(map[string]string{"known": "A > B"})
	_, err = be.Splice(buf, m, "unknown", []byte("x"))
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Splice(unknown-name): want ErrBlockNotFound, got %v", err)
	}

	// Known name, no matching heading.
	mMiss := newManifest(map[string]string{"missing": "Nope > NotHere"})
	_, err = be.Splice(buf, mMiss, "missing", []byte("x"))
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("Splice(no-heading-match): want ErrBlockNotFound, got %v", err)
	}
}

// TestMdExplicitBackend_ParseNilManifest pins the nil-manifest contract
// for Parse: returns an empty Blocks slice with no error. Mirrors the
// html backend.
func TestMdExplicitBackend_ParseNilManifest(t *testing.T) {
	be := &Backend{}
	blocks, err := be.Parse([]byte("# A\nbody\n"), nil)
	if err != nil {
		t.Fatalf("Parse(nil manifest): %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("Parse(nil manifest): want 0 blocks, got %d", len(blocks))
	}
}

// TestMdExplicitBackend_MarshalEmpty: an empty Blocks slice marshals
// to a zero-length non-nil byte slice (mirrors the html backend's
// zero-block contract).
func TestMdExplicitBackend_MarshalEmpty(t *testing.T) {
	be := &Backend{}
	out, err := be.Marshal(format.Blocks{}, nil)
	if err != nil {
		t.Fatalf("Marshal(empty): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Marshal(empty): want zero-length output, got %q", out)
	}
}
