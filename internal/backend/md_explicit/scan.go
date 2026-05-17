// Package md_explicit implements a Format backend that addresses markdown
// blocks by explicit heading-path selectors (e.g. "Introduction > Goals").
// It is deliberately disjoint from internal/backend/md/ — that package
// addresses sections via slug-derived record addresses; this package
// matches against literal heading-text bytes along an ATX heading path.
//
// This file owns the line-oriented scanner + selector substrate used by
// the Format implementation in backend.go. No exported scanner API
// reaches outside the package — package-local function calls only.
//
// Selector contract (drop_004 L3-D3 amendments 1, 3, 4, 5, 8):
//
//   - Segments are separated by " > " (space-greater-space).
//   - Segment text is matched byte-literal against heading text
//     (including inline-formatting bytes — `**Goals**` selector matches
//     `## **Goals**` heading, NOT `Goals`).
//   - Selector segments must bind to a DEPTH-ADJACENT run of headings:
//     segment N+1 must be exactly one level deeper than segment N.
//     A selector may start at any depth (1..6).
//   - Code-fence content (``` or ~~~ runs of 3+) is excluded — heading-
//     like lines inside fences are NOT headings.
//   - Backslash at column 0 (e.g. "\#") suppresses heading detection
//     for that line.
//   - On no-match, callers surface format.ErrBlockNotFound.
package md_explicit

import (
	"strings"

	"github.com/evanmschultz/ta/internal/format"
)

// HeadingNode is one ATX heading discovered by WalkHeadings.
//
// Depth is the heading level (1..6).
//
// Text is the raw heading text bytes between the required space after
// the hash run and the end of the heading line, with surrounding ASCII
// whitespace trimmed. Inline-formatting bytes (`**...**`, `_..._`,
// backticks, etc.) are PRESERVED literal so byte-identity round-trip
// holds and selector match is unambiguous.
//
// ByteRange is the [start, end) byte offsets of this heading's section
// span — from the heading's own line start to the start of the next
// heading at the SAME OR SHALLOWER depth, or EOF for the last such
// heading. Deeper headings under this one are body bytes of this node
// AND addressable in their own right with narrower nested ranges.
//
// HeaderLineEnd is the offset of the byte JUST AFTER the heading's own
// newline (or EOF). Callers needing "body bytes only" can slice
// buf[HeaderLineEnd:ByteRange[1]].
type HeadingNode struct {
	Depth         int
	Text          string
	ByteRange     [2]int
	HeaderLineEnd int
}

// WalkHeadings scans buf and returns every ATX heading in source order.
// Fence-aware: heading-like lines inside ``` or ~~~ fences are skipped.
// Escape-aware: a line starting with "\#" is not a heading.
// Stdlib-only.
func WalkHeadings(buf []byte) []HeadingNode {
	var out []HeadingNode
	n := len(buf)
	if n == 0 {
		return out
	}

	// Fence state.
	inFence := false
	fenceChar := byte(0)
	fenceLen := 0

	lineStart := 0
	for lineStart <= n {
		// Find end of this line.
		lineEnd := lineStart
		for lineEnd < n && buf[lineEnd] != '\n' {
			lineEnd++
		}
		// nextLine = byte after the '\n' (or n at EOF).
		nextLine := lineEnd
		if nextLine < n && buf[nextLine] == '\n' {
			nextLine++
		}

		// Fence detection runs first — fence markers are not headings.
		if fc, flen, ok := readFenceLine(buf, lineStart, lineEnd); ok {
			if inFence {
				if fc == fenceChar && flen >= fenceLen {
					inFence = false
					fenceChar = 0
					fenceLen = 0
				}
			} else {
				inFence = true
				fenceChar = fc
				fenceLen = flen
			}
		} else if !inFence {
			if depth, text, ok := readATXHeading(buf, lineStart, lineEnd); ok {
				out = append(out, HeadingNode{
					Depth:         depth,
					Text:          text,
					ByteRange:     [2]int{lineStart, 0},
					HeaderLineEnd: nextLine,
				})
			}
		}

		if nextLine == lineStart {
			// Empty buffer slice at EOF.
			break
		}
		if lineEnd == n && nextLine == n {
			// Final line without trailing newline already processed.
			break
		}
		lineStart = nextLine
	}

	// Patch ByteRange[1]: end at next heading whose Depth <= self.Depth.
	// If none found, EOF.
	for idx := range out {
		end := n
		for j := idx + 1; j < len(out); j++ {
			if out[j].Depth <= out[idx].Depth {
				end = out[j].ByteRange[0]
				break
			}
		}
		out[idx].ByteRange[1] = end
	}

	return out
}

// ParseSelector splits selector on " > " producing literal segments.
// Surrounding whitespace within each segment is preserved (byte-literal
// match per amendment 5). Empty string returns nil.
func ParseSelector(selector string) []string {
	if selector == "" {
		return nil
	}
	return strings.Split(selector, " > ")
}

// FindByPath resolves selector against the heading tree in buf and
// returns the matched heading plus its body bytes (heading line +
// content through ByteRange end). Returns format.ErrBlockNotFound on
// no match.
//
// Match rule (drop_004 L3-D3 amendments 1, 4, 5): selector segments
// must bind to a depth-adjacent ascending run of headings in source
// order. Segment 1 binds to a heading at any depth D1; segment N+1
// binds to a heading at depth = D1 + N appearing AFTER segment N's
// heading and BEFORE any heading at depth <= D1 (which would close
// segment 1's subtree). Segment text matches heading text byte-literal.
func FindByPath(buf []byte, selector string) (HeadingNode, []byte, error) {
	segments := ParseSelector(selector)
	if len(segments) == 0 {
		return HeadingNode{}, nil, format.ErrBlockNotFound
	}
	headings := WalkHeadings(buf)
	if len(headings) == 0 {
		return HeadingNode{}, nil, format.ErrBlockNotFound
	}

	// For each heading whose text matches segment[0], attempt to match
	// the remaining segments in strict depth-adjacent order.
	for i, h := range headings {
		if h.Text != segments[0] {
			continue
		}
		matched, idx := matchRemaining(headings, i, segments)
		if matched {
			final := headings[idx]
			return final, buf[final.ByteRange[0]:final.ByteRange[1]], nil
		}
	}

	return HeadingNode{}, nil, format.ErrBlockNotFound
}

// matchRemaining walks segments[1:] against headings starting at
// index startIdx (which already matched segments[0]). Returns (true,
// indexOfFinalHeading) on full match.
//
// For each subsequent segment, walk forward looking for a heading at
// exactly depth = startDepth + segIndex with text == segment. Stop and
// fail if we encounter a heading at depth <= startDepth (subtree
// closed) before finding the next segment.
func matchRemaining(headings []HeadingNode, startIdx int, segments []string) (bool, int) {
	startDepth := headings[startIdx].Depth
	curIdx := startIdx
	for segIdx := 1; segIdx < len(segments); segIdx++ {
		wantDepth := startDepth + segIdx
		wantText := segments[segIdx]
		found := false
		for j := curIdx + 1; j < len(headings); j++ {
			// Subtree boundary: a heading at depth <= startDepth closes
			// the first-segment subtree — give up.
			if headings[j].Depth <= startDepth {
				return false, -1
			}
			if headings[j].Depth == wantDepth && headings[j].Text == wantText {
				curIdx = j
				found = true
				break
			}
			// Headings between startDepth+1 and wantDepth-1 may appear
			// (e.g. interleaved siblings) — skip them.
			// Headings deeper than wantDepth before finding wantDepth
			// are also skipped (they belong to a different sibling
			// subtree at intermediate level).
		}
		if !found {
			return false, -1
		}
	}
	return true, curIdx
}

// readATXHeading tries to read an ATX heading occupying buf[lineStart:lineEnd].
// Returns depth (1..6), raw heading text, and true on success.
//
// Rules:
//
//   - Optional ASCII-whitespace prefix is NOT allowed — hash must be at col 0
//     (consistent with internal/backend/md/ scanner).
//   - Backslash at lineStart suppresses heading (escape rule, amendment 6).
//   - 1..6 `#` chars followed by at least one space or tab.
//   - Heading text is everything after the whitespace through end-of-line,
//     with trailing whitespace trimmed. Inline-formatting bytes preserved.
//   - Empty heading text is not a heading.
func readATXHeading(buf []byte, lineStart, lineEnd int) (int, string, bool) {
	if lineStart >= lineEnd {
		return 0, "", false
	}
	// Escape: backslash at col 0 suppresses heading detection.
	if buf[lineStart] == '\\' {
		return 0, "", false
	}
	i := lineStart
	depth := 0
	for i < lineEnd && buf[i] == '#' && depth < 7 {
		depth++
		i++
	}
	if depth == 0 || depth > 6 {
		return 0, "", false
	}
	if i >= lineEnd {
		return 0, "", false
	}
	if buf[i] != ' ' && buf[i] != '\t' {
		return 0, "", false
	}
	// Advance past whitespace after the hash run.
	for i < lineEnd && (buf[i] == ' ' || buf[i] == '\t') {
		i++
	}
	// Trim trailing ASCII whitespace from the heading text. We do NOT
	// strip trailing `#` runs — amendment 5 requires byte-literal
	// preservation, and matching is text-equal so trailing decoration
	// would simply not match a clean selector. Users own their headings.
	end := lineEnd
	for end > i && (buf[end-1] == ' ' || buf[end-1] == '\t' || buf[end-1] == '\r') {
		end--
	}
	if end <= i {
		return 0, "", false
	}
	return depth, string(buf[i:end]), true
}

// readFenceLine detects a fence-open-or-close line in buf[lineStart:lineEnd].
// Returns fence char and run length when matched. A fence is 3+
// consecutive '`' or '~' at column 0, optionally followed by an info
// string.
func readFenceLine(buf []byte, lineStart, lineEnd int) (byte, int, bool) {
	if lineStart >= lineEnd {
		return 0, 0, false
	}
	c := buf[lineStart]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	i := lineStart
	runLen := 0
	for i < lineEnd && buf[i] == c {
		runLen++
		i++
	}
	if runLen < 3 {
		return 0, 0, false
	}
	return c, runLen, true
}
