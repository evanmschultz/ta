// L3-D4-D2 wires the regex engine in regex.go into the format.Format
// contract from internal/format. Backend is registered under the
// schema-enum key "txt" at package init so format.Get("txt") resolves
// it without further wiring.
//
// # Manifest contract (CE-5)
//
// The txt backend consumes pre-compiled *regexp.Regexp values from the
// manifest loader; it NEVER recompiles a pattern per Find/Splice call.
// *format.TxtManifest.Compiled holds the block-name → *regexp.Regexp map
// produced at LoadManifestFile time. Malformed patterns surface as
// loader errors, not Find/Splice errors.
//
// # Anchor semantics
//
// Go's default `^` / `$` match start-of-text and end-of-text, NOT
// start-of-line / end-of-line. Manifest authors writing multi-line plain
// text patterns typically need the `(?m)` flag to anchor against line
// boundaries. Without `(?m)`, `^HEADER:.*$` matches only when the buffer
// itself begins with `HEADER:` and ends with the closing line — which is
// almost never the intent. The backend does not rewrite or correct
// missing `(?m)`; authors own their patterns.
//
// # Zero-width match collapse (post-CE)
//
// FindBlock collapses zero-width single matches (e.g. anchor-only `^`
// patterns) to format.ErrBlockNotFound. The backend's Find / Splice path
// inherits this: a successful FindBlock return ALWAYS carries non-empty
// Block.Bytes, and Splice on an anchor-only pattern is a not-found error
// rather than a silent insertion at the anchor offset.
//
// # Round-trip semantic (CE-6)
//
// "Round-trip byte-identical" for txt = Parse(buf,m) → Marshal(blocks,m)
// returns bytes byte-EQUAL to the original buf, ONLY when no Block.Bytes
// was edited between Parse and Marshal AND the manifest's regex selectors
// cover the entire buffer with no inter-block gaps. When gaps exist,
// Marshal output omits them (mirrors html backend's documented lossy
// round-trip). Splice provides the in-place edit path that preserves
// unedited regions byte-for-byte regardless of inter-block gaps.

package txt

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/evanmschultz/ta/internal/format"
)

// Backend implements format.Format for plain-text inputs via regex selectors.
// Backend is stateless — a single zero-value instance is registered with
// format.Register at package init.
type Backend struct{}

// compile-time guarantee: Backend satisfies format.Format. Mirrors the
// pattern used by the html backend and the format package's own test mocks.
var _ format.Format = (*Backend)(nil)

func init() {
	format.Register("txt", &Backend{})
}

// compiledLookup is the minimal manifest surface Backend needs beyond
// format.Manifest itself: a name → *regexp.Regexp lookup for compiled
// patterns. Implemented by *format.TxtManifest via the adapter below;
// other Manifest impls that want to drive this backend can satisfy this
// interface directly.
type compiledLookup interface {
	// CompiledFor returns the pre-compiled regex for the given block
	// name, or (nil, false) when name is unknown.
	CompiledFor(name string) (*regexp.Regexp, bool)
}

// txtManifestAdapter wraps *format.TxtManifest's Compiled map as the
// compiledLookup interface above. Keeps backend.go independent of the
// concrete manifest struct shape.
type txtManifestAdapter struct {
	m *format.TxtManifest
}

func (a txtManifestAdapter) CompiledFor(name string) (*regexp.Regexp, bool) {
	if a.m == nil || a.m.Compiled == nil {
		return nil, false
	}
	re, ok := a.m.Compiled[name]
	return re, ok
}

// resolveCompiled pulls a compiled regex for the given manifest block name
// out of the caller-supplied Manifest. Recognises the concrete TxtManifest
// type and the compiledLookup interface; returns (nil, false) otherwise.
func resolveCompiled(m format.Manifest, name string) (*regexp.Regexp, bool) {
	switch v := m.(type) {
	case compiledLookup:
		return v.CompiledFor(name)
	case *format.TxtManifest:
		return txtManifestAdapter{m: v}.CompiledFor(name)
	}
	return nil, false
}

// Parse extracts every manifest-named block from buf. For each block name
// in the manifest, the corresponding pre-compiled regex is run via
// FindBlock; matches contribute a Block whose Bytes are a sub-slice of buf
// (zero-copy). Blocks are returned ordered by Start byte.
//
// Block lookups that return format.ErrBlockNotFound silently contribute
// zero blocks — Parse is not the validation surface, the manifest loader
// is. format.ErrAmbiguousMatch is propagated as a real error: an
// ambiguous pattern is a manifest-author bug that Parse must surface so
// the caller sees the tightening hint.
//
// Overlap policy: when two selectors match distinct-but-overlapping byte
// ranges, each contributes its own Block — Parse does NOT dedup overlap.
// Blocks are sorted by Start so callers see overlapping regions in source
// order and can act on them.
//
// Multi-ambiguous policy: when more than one selector is ambiguous in the
// same Parse call, the FIRST encountered (manifest iteration order) wins
// and short-circuits the loop — Parse returns one wrapped
// format.ErrAmbiguousMatch and does NOT errors.Join sibling ambiguities.
// Tighten one selector at a time.
func (b *Backend) Parse(buf []byte, m format.Manifest) (format.Blocks, error) {
	if m == nil {
		return format.Blocks{}, nil
	}
	// Build the (block-name, compiled-regex) iteration list. Names sorted
	// for reproducible iteration order; Block-Start sort below produces
	// the final output order regardless.
	type entry struct {
		name string
		re   *regexp.Regexp
	}
	var entries []entry
	for _, sel := range m.Selectors() {
		name, ok := m.BlockName(sel)
		if !ok {
			continue
		}
		re, ok := resolveCompiled(m, name)
		if !ok || re == nil {
			// Manifest advertises the selector but the loader did not
			// expose a compiled pattern — surface as a manifest internal
			// inconsistency that the loader should have caught. Skip so
			// Parse stays defensive (mirrors html backend's silent skip
			// of BlockName-resolves-empty entries).
			continue
		}
		entries = append(entries, entry{name: name, re: re})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var hits format.Blocks
	for _, e := range entries {
		block, _, err := FindBlock(buf, e.name, e.re)
		if err != nil {
			if errors.Is(err, format.ErrBlockNotFound) {
				continue
			}
			// ErrAmbiguousMatch + nil-regex propagate.
			return nil, fmt.Errorf("txt backend: parse: block %q: %w", e.name, err)
		}
		hits = append(hits, block)
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Start < hits[j].Start })
	return hits, nil
}

// Find returns the byte range of the named block in buf, resolved via the
// manifest's compiled-regex map. Returns format.ErrBlockNotFound when name
// is unknown to the manifest or when the pattern matches zero candidates
// (or a single zero-width match — see FindBlock). Returns
// format.ErrAmbiguousMatch when the pattern matches more than one
// candidate; the manifest author must tighten the pattern.
//
// The returned []byte is a sub-slice of buf (zero-copy) per the FindBlock
// substrate contract.
func (b *Backend) Find(buf []byte, m format.Manifest, name string) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("txt backend: find: name %q: %w", name, format.ErrBlockNotFound)
	}
	re, ok := resolveCompiled(m, name)
	if !ok || re == nil {
		return nil, fmt.Errorf("txt backend: find: name %q: %w", name, format.ErrBlockNotFound)
	}
	block, _, err := FindBlock(buf, name, re)
	if err != nil {
		return nil, fmt.Errorf("txt backend: find: name %q: %w", name, err)
	}
	return block.Bytes, nil
}

// Splice replaces the named block's byte range in buf with content,
// preserving all bytes outside [Block.Start, Block.End) byte-for-byte.
// Returns a NEW slice — buf is not mutated. Idempotent: calling Splice
// twice with the same content produces a buffer byte-equal to the
// first-Splice result (the second call finds the just-written content
// and overwrites it with the same bytes).
//
// Sentinels: format.ErrBlockNotFound when name is unknown or the pattern
// matches zero candidates; format.ErrAmbiguousMatch when the pattern
// matches more than one candidate.
func (b *Backend) Splice(buf []byte, m format.Manifest, name string, content []byte) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("txt backend: splice: name %q: %w", name, format.ErrBlockNotFound)
	}
	re, ok := resolveCompiled(m, name)
	if !ok || re == nil {
		return nil, fmt.Errorf("txt backend: splice: name %q: %w", name, format.ErrBlockNotFound)
	}
	block, _, err := FindBlock(buf, name, re)
	if err != nil {
		return nil, fmt.Errorf("txt backend: splice: name %q: %w", name, err)
	}
	// Build the result in a freshly-allocated slice so the caller's buf
	// stays untouched. Capacity is exact — no over-allocation.
	out := make([]byte, 0, block.Start+len(content)+(len(buf)-block.End))
	out = append(out, buf[:block.Start]...)
	out = append(out, content...)
	out = append(out, buf[block.End:]...)
	return out, nil
}

// Marshal concatenates each block's Bytes in source order (sorted by
// Start). When the input Blocks slice was produced by Parse against a
// buffer whose manifest patterns cover the buffer without inter-block
// gaps AND no Block.Bytes has been edited, the output is byte-equal to
// the original buffer (CE-6 round-trip identity, no-edit path). When
// gaps exist between matched blocks, the gap bytes are omitted from the
// output — mirrors html backend's documented lossy round-trip behavior.
//
// Marshal does not consult m; it is accepted for interface uniformity.
func (b *Backend) Marshal(blocks format.Blocks, _ format.Manifest) ([]byte, error) {
	if len(blocks) == 0 {
		return []byte{}, nil
	}
	ordered := make(format.Blocks, len(blocks))
	copy(ordered, blocks)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })
	total := 0
	for _, blk := range ordered {
		total += len(blk.Bytes)
	}
	out := make([]byte, 0, total)
	for _, blk := range ordered {
		out = append(out, blk.Bytes...)
	}
	return out, nil
}
