// Package txt implements the format.Format contract for plain-text inputs
// using regex selectors. Manifest authors declare one regex per block name
// (compiled at manifest load time per L3-D1 substrate contract); the engine
// in this file runs the pre-compiled pattern against a buffer and surfaces
// the matched byte-range plus any named capture groups.
//
// This file owns ONLY the low-level regex engine. The Format interface
// implementation (Parse / Find / Splice / Marshal) plus package init-time
// registration live in backend.go (L3-D4-D2 builder slice).
package txt

import (
	"fmt"
	"regexp"

	"github.com/evanmschultz/ta/internal/format"
)

// FindBlock runs the pre-compiled regex against buf and returns a single
// matched block plus its named capture groups. Multi-match is a contract
// violation (the manifest author's regex is too broad); the engine returns
// format.ErrAmbiguousMatch in that case so the caller can surface a clear
// tightening hint. Zero-match returns format.ErrBlockNotFound.
//
// Zero-width matches (e.g. anchor-only patterns like `^` against multi-line
// buffers) collapse to format.ErrBlockNotFound — a single zero-byte match
// has no useful block to surface, and silent success would let downstream
// Splice insert content at the anchor position with no signal to the caller.
//
// The returned format.Block.Bytes is a sub-slice of buf (NOT a copy) — the
// backend's Splice layer relies on the Start/End byte offsets to preserve
// the surrounding context. Named capture groups are returned as
// name → []byte sub-slice of buf, keyed by the regex's (?P<name>...)
// declarations. Unnamed groups are NOT returned; only named ones matter for
// downstream field extraction.
//
// The compiled regex is consumed by reference; it is NEVER recompiled here
// (L3-D4 CE-5 contract: compile happens at manifest load time, NOT per
// Find/Splice). Callers MUST pre-compile.
func FindBlock(buf []byte, name string, re *regexp.Regexp) (format.Block, map[string][]byte, error) {
	if re == nil {
		return format.Block{}, nil, fmt.Errorf("txt: nil regex for block %q (manifest loader must pre-compile)", name)
	}
	matches := re.FindAllSubmatchIndex(buf, -1)
	if len(matches) == 0 {
		return format.Block{}, nil, format.ErrBlockNotFound
	}
	if len(matches) > 1 {
		return format.Block{}, nil, fmt.Errorf("txt: regex for block %q matched %d candidates: %w", name, len(matches), format.ErrAmbiguousMatch)
	}
	m := matches[0]
	start, end := m[0], m[1]
	if start == end {
		return format.Block{}, nil, format.ErrBlockNotFound
	}
	block := format.Block{
		Name:  name,
		Bytes: buf[start:end],
		Start: start,
		End:   end,
	}
	captures := extractNamedGroups(buf, re, m)
	return block, captures, nil
}

// extractNamedGroups walks the match indices and pulls each (?P<name>...)
// group's byte-range out of buf into the returned map. Groups that did not
// participate in the match (indices == -1) are omitted from the result.
// An empty (no named groups in pattern) result is nil, NOT an empty map, so
// callers can cheaply check `if captures == nil`.
func extractNamedGroups(buf []byte, re *regexp.Regexp, match []int) map[string][]byte {
	names := re.SubexpNames()
	if len(names) <= 1 {
		// SubexpNames[0] is always the empty whole-match slot; if it's the
		// only entry, the pattern has no subgroups at all.
		return nil
	}
	out := make(map[string][]byte)
	for i := 1; i < len(names); i++ {
		name := names[i]
		if name == "" {
			// Unnamed subgroup — skip per CE-2 contract (only named ones
			// matter downstream).
			continue
		}
		s, e := match[2*i], match[2*i+1]
		if s < 0 || e < 0 {
			// Group did not participate in the match (e.g. alternation).
			continue
		}
		out[name] = buf[s:e]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
