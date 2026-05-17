package txt

import (
	"bytes"
	"errors"
	"regexp"
	"testing"

	"github.com/evanmschultz/ta/internal/format"
)

// TestTxtRegex_NamedGroupCapture pins CE-2: the engine extracts each
// (?P<name>...) group from the match as a name → []byte byte-range map
// keyed off the regex's declared subgroup names. Unnamed groups are NOT
// surfaced.
func TestTxtRegex_NamedGroupCapture(t *testing.T) {
	buf := []byte("title: The Phantom Tollbooth\nauthor: Norton Juster\n")
	re := regexp.MustCompile(`(?m)^title:\s+(?P<title>.+)$`)

	block, captures, err := FindBlock(buf, "title_line", re)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	if block.Name != "title_line" {
		t.Errorf("block.Name = %q, want %q", block.Name, "title_line")
	}
	// Byte-range must point at the actual match span in buf.
	if !bytes.Equal(block.Bytes, []byte("title: The Phantom Tollbooth")) {
		t.Errorf("block.Bytes = %q, want %q", block.Bytes, "title: The Phantom Tollbooth")
	}
	if block.Start != 0 || block.End != len("title: The Phantom Tollbooth") {
		t.Errorf("block byte-range = [%d,%d), want [0,%d)", block.Start, block.End, len("title: The Phantom Tollbooth"))
	}
	// Named capture must hold the (?P<title>...) sub-slice.
	if captures == nil {
		t.Fatalf("captures = nil, want non-nil map with %q", "title")
	}
	got, ok := captures["title"]
	if !ok {
		t.Fatalf("captures missing key %q; got keys %v", "title", keysOf(captures))
	}
	if !bytes.Equal(got, []byte("The Phantom Tollbooth")) {
		t.Errorf("captures[title] = %q, want %q", got, "The Phantom Tollbooth")
	}
}

// TestTxtRegex_NamedGroupCapture_UnnamedGroupsSkipped pins the contract
// that unnamed (...) subgroups in the manifest pattern are NOT included
// in the returned capture map. Only (?P<name>...) groups surface.
func TestTxtRegex_NamedGroupCapture_UnnamedGroupsSkipped(t *testing.T) {
	buf := []byte("key=value")
	re := regexp.MustCompile(`^(\w+)=(?P<value>\w+)$`)
	_, captures, err := FindBlock(buf, "kv", re)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	if captures == nil {
		t.Fatal("captures nil; want at least the named group")
	}
	if _, has := captures["value"]; !has {
		t.Errorf("captures missing %q; got %v", "value", keysOf(captures))
	}
	if len(captures) != 1 {
		t.Errorf("captures has %d entries, want 1 (only the named group); keys = %v", len(captures), keysOf(captures))
	}
}

// TestTxtRegex_NamedGroupCapture_NoGroups pins that a pattern without any
// subgroups returns captures == nil (cheap nil-check at call sites).
func TestTxtRegex_NamedGroupCapture_NoGroups(t *testing.T) {
	buf := []byte("hello world")
	re := regexp.MustCompile(`hello world`)
	_, captures, err := FindBlock(buf, "greeting", re)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	if captures != nil {
		t.Errorf("captures = %v, want nil for group-less pattern", captures)
	}
}

// TestTxtRegex_MultiMatchErrAmbiguous pins CE-1: when the pre-compiled
// regex matches MORE than one place in the buffer, the engine returns
// format.ErrAmbiguousMatch (NOT first-match). Manifest authors must
// tighten the pattern.
func TestTxtRegex_MultiMatchErrAmbiguous(t *testing.T) {
	buf := []byte("line one\nline two\nline three\n")
	// Pattern matches all three "line X" lines — that is the ambiguous
	// case the substrate forbids for selector engines.
	re := regexp.MustCompile(`(?m)^line \w+$`)

	_, _, err := FindBlock(buf, "lines", re)
	if err == nil {
		t.Fatal("FindBlock with multi-match pattern returned nil error; want format.ErrAmbiguousMatch")
	}
	if !errors.Is(err, format.ErrAmbiguousMatch) {
		t.Errorf("errors.Is(err, format.ErrAmbiguousMatch) = false; err = %v", err)
	}
	// Belt-and-braces: must NOT collide with ErrBlockNotFound.
	if errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("errors.Is(err, format.ErrBlockNotFound) = true; sibling sentinel must not match")
	}
}

// TestTxtRegex_CaseInsensitiveFlag pins the flag pass-through contract:
// the manifest loader's compiled pattern carries flags (e.g. `(?i)`) into
// the engine — the engine itself does NOT recompile or strip flags. A
// manifest author declaring `(?i)title:` MUST match "TITLE:" in the
// input. (Adjacent to CE-5's no-recompile contract, but the load-time
// compile itself is pinned by TestManifest_TxtRegexCompiledAtLoadTime in
// the format package; this test pins the engine's flag-respecting consume
// path specifically.)
func TestTxtRegex_CaseInsensitiveFlag(t *testing.T) {
	buf := []byte("TITLE: Important Document")
	re := regexp.MustCompile(`(?i)^title:\s+(?P<value>.+)$`)

	block, captures, err := FindBlock(buf, "title", re)
	if err != nil {
		t.Fatalf("FindBlock with (?i) flag: %v", err)
	}
	if !bytes.Equal(block.Bytes, []byte("TITLE: Important Document")) {
		t.Errorf("block.Bytes = %q, want %q (case-insensitive match must surface the original-cased bytes from buf)", block.Bytes, "TITLE: Important Document")
	}
	got, ok := captures["value"]
	if !ok {
		t.Fatalf("captures missing key %q; got %v", "value", keysOf(captures))
	}
	if !bytes.Equal(got, []byte("Important Document")) {
		t.Errorf("captures[value] = %q, want %q", got, "Important Document")
	}
}

// TestTxtRegex_ZeroMatchErrBlockNotFound pins CE-4: when the regex matches
// nothing, the engine returns the substrate sentinel format.ErrBlockNotFound
// (reused from internal/format/format.go:53).
func TestTxtRegex_ZeroMatchErrBlockNotFound(t *testing.T) {
	buf := []byte("nothing relevant here\n")
	re := regexp.MustCompile(`^summary:\s+(?P<v>.+)$`)
	_, _, err := FindBlock(buf, "summary", re)
	if err == nil {
		t.Fatal("FindBlock on no-match input returned nil error; want format.ErrBlockNotFound")
	}
	if !errors.Is(err, format.ErrBlockNotFound) {
		t.Errorf("errors.Is(err, format.ErrBlockNotFound) = false; err = %v", err)
	}
	if errors.Is(err, format.ErrAmbiguousMatch) {
		t.Errorf("errors.Is(err, format.ErrAmbiguousMatch) = true; sibling sentinel must not match")
	}
}

// TestTxtRegex_ZeroWidthMatchCollapsesToErrBlockNotFound pins the L3-D4-D1
// build-QA falsif CE: a single zero-width match (e.g. anchor-only patterns
// like `^` against a multi-line buffer) must NOT silently return a successful
// empty block — that would let downstream Splice insert content at the anchor
// position with no signal. Engine collapses zero-width single matches to
// format.ErrBlockNotFound so callers see the same not-actionable signal as
// the zero-match case.
func TestTxtRegex_ZeroWidthMatchCollapsesToErrBlockNotFound(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		buf     string
	}{
		{"caret on multi-line buffer", `^`, "ab\ncd"},
		{"caret on empty buffer", `^`, ""},
		{"dollar on multi-line buffer", `$`, "ab\ncd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)
			_, _, err := FindBlock([]byte(tc.buf), "zw", re)
			if err == nil {
				t.Fatalf("FindBlock with zero-width single match returned nil error; want format.ErrBlockNotFound")
			}
			if !errors.Is(err, format.ErrBlockNotFound) {
				t.Errorf("errors.Is(err, format.ErrBlockNotFound) = false; err = %v", err)
			}
		})
	}
}

// TestTxtRegex_NilRegexIsError pins the defensive guard for the manifest
// loader contract: if (for any reason) a nil *regexp.Regexp reaches the
// engine, the engine returns a descriptive error rather than panicking.
// The manifest loader is responsible for pre-compilation; a nil here means
// the loader broke its contract.
func TestTxtRegex_NilRegexIsError(t *testing.T) {
	_, _, err := FindBlock([]byte("anything"), "x", nil)
	if err == nil {
		t.Fatal("FindBlock(nil regex) returned nil error; want descriptive failure")
	}
	// Neither substrate sentinel applies — nil regex is a loader-contract
	// bug, not a match outcome.
	if errors.Is(err, format.ErrBlockNotFound) || errors.Is(err, format.ErrAmbiguousMatch) {
		t.Errorf("nil-regex error must NOT alias substrate match sentinels; err = %v", err)
	}
}

// keysOf is a tiny test helper: returns a slice of map keys for assertion
// messages. Order is not guaranteed (map iteration); fine for failure msgs.
func keysOf(m map[string][]byte) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
