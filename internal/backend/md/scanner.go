package md

import (
	"fmt"
	"strings"
)

// Heading is one ATX heading discovered by scanATX.
//
// LineStart / LineEnd are 1-indexed inclusive line numbers for the
// heading's own line (LineEnd == LineStart unless a future multi-line
// heading form is ever added).
//
// ByteRange is the [start, end) byte offsets in the source buffer of
// this heading's entire section span — from the beginning of the
// heading's line to the start of the next heading of ANY depth, or
// EOF. This is the "flat" model from V2-PLAN §5.3.2.
type Heading struct {
	Level     int
	Text      string
	Slug      string
	LineStart int
	LineEnd   int
	ByteRange [2]int
}

// scanATX walks buf and returns every recognised ATX heading in source
// order. It tracks fenced-code-block state so `#` lines inside fences
// are treated as content.
//
// Collision detection: if two headings at the same level produce the
// same slug in one file, scanATX returns ErrSlugCollision (wrapped
// with both line numbers). Collisions across different levels are
// allowed — each schema type binds to a specific level, so same slug
// at different levels cannot alias a single address.
func scanATX(buf []byte) ([]Heading, error) {
	var out []Heading
	// Fence-state: when inFence is true, char is the fence char and
	// runLen is the opener length. A closing fence must match char and
	// have runLen' >= runLen.
	inFence := false
	fenceChar := byte(0)
	fenceLen := 0

	line := 1
	lineStart := 0
	n := len(buf)

	for i := 0; i <= n; i++ {
		// At a line start when i == 0 or buf[i-1] == '\n'.
		if i == 0 || (i > 0 && buf[i-1] == '\n') {
			lineStart = i
			// Try to match fence opener/closer first.
			if fc, flen, ok := readFenceLine(buf, lineStart); ok {
				if inFence {
					// Close only if char matches and length is >=.
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
				// Advance scanner to end of line via the outer loop;
				// continue iterating normally.
			} else if !inFence {
				// Try to match an ATX heading at col 0.
				if lvl, text, ok := readATXHeading(buf, lineStart); ok {
					slug := slugFromHeading(text)
					if slug != "" {
						h := Heading{
							Level:     lvl,
							Text:      text,
							Slug:      slug,
							LineStart: line,
							LineEnd:   line,
						}
						// ByteRange.Start = lineStart; end is patched later.
						h.ByteRange[0] = lineStart
						out = append(out, h)
					}
				}
			}
		}
		if i == n {
			break
		}
		if buf[i] == '\n' {
			line++
		}
	}

	// Patch ByteRange.End for each heading: start of next heading of
	// ANY level, or EOF for the last.
	for idx := range out {
		if idx+1 < len(out) {
			out[idx].ByteRange[1] = out[idx+1].ByteRange[0]
		} else {
			out[idx].ByteRange[1] = n
		}
	}

	// Collision check: by (level, slug).
	type key struct {
		level int
		slug  string
	}
	seen := map[key]int{} // key -> first-seen line
	for _, h := range out {
		k := key{h.Level, h.Slug}
		if first, dup := seen[k]; dup {
			return nil, fmt.Errorf("%w: level=H%d slug=%q at lines %d and %d",
				ErrSlugCollision, h.Level, h.Slug, first, h.LineStart)
		}
		seen[k] = h.LineStart
	}

	return out, nil
}

// readATXHeading tries to read an ATX heading starting at buf[lineStart].
// Returns level, heading text (trim-spaced, trailing-hash stripped),
// and true on success. Rules:
//
//   - 1..6 `#` at col 0
//   - followed by at least one space or tab
//   - heading text is everything up to LF/EOF, with trailing `#` run
//     stripped, trimmed of surrounding whitespace
//   - empty trimmed text is not a heading
func readATXHeading(buf []byte, lineStart int) (int, string, bool) {
	i := lineStart
	n := len(buf)
	level := 0
	for i < n && buf[i] == '#' && level < 7 {
		level++
		i++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	if i >= n {
		return 0, "", false
	}
	// Must have at least one space or tab after the hashes.
	if buf[i] != ' ' && buf[i] != '\t' {
		return 0, "", false
	}
	// Advance past whitespace.
	for i < n && (buf[i] == ' ' || buf[i] == '\t') {
		i++
	}
	// Read to EOL.
	lineEnd := i
	for lineEnd < n && buf[lineEnd] != '\n' {
		lineEnd++
	}
	text := string(buf[i:lineEnd])
	// Strip trailing hash run (with optional preceding whitespace).
	text = stripTrailingHashes(text)
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

// readFenceLine detects a fence-open-or-close line starting at
// buf[lineStart]. Returns the fence character and run length if this
// line is a fence marker. A fence is 3+ consecutive ` or ~, optionally
// followed by "info string" after whitespace.
func readFenceLine(buf []byte, lineStart int) (byte, int, bool) {
	i := lineStart
	n := len(buf)
	if i >= n {
		return 0, 0, false
	}
	c := buf[i]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	runLen := 0
	for i < n && buf[i] == c {
		runLen++
		i++
	}
	if runLen < 3 {
		return 0, 0, false
	}
	// Everything after the fence run and until EOL is the info
	// string; it's allowed to be anything (we don't care). We just
	// need the rest of the line to not contain another fence-char run
	// that would confuse us. CommonMark says a closing fence must not
	// have any info string, but in practice content lines like
	// "```go" should be recognised as openers/closers both.
	return c, runLen, true
}

// stripTrailingHashes removes a trailing run of `#` characters that is
// either at end-of-string or preceded by whitespace. CommonMark rule:
// trailing hashes are optional decoration and not part of the heading
// text.
func stripTrailingHashes(s string) string {
	// Trim trailing whitespace temporarily to locate the hash run.
	trimmed := strings.TrimRight(s, " \t")
	end := len(trimmed)
	// Count trailing hashes.
	j := end
	for j > 0 && trimmed[j-1] == '#' {
		j--
	}
	if j == end {
		return s // no trailing hashes
	}
	// Trailing-hash run is valid only if preceded by whitespace or at
	// start-of-line (the whole trimmed string is hashes).
	if j > 0 && trimmed[j-1] != ' ' && trimmed[j-1] != '\t' {
		return s
	}
	return strings.TrimRight(trimmed[:j], " \t")
}

// slugFromHeading lowercases heading text, replaces non-alphanumeric
// runs with a single hyphen, and trims surrounding hyphens. Non-ASCII
// characters are stripped (this is deliberate — stable across OSes
// without a normalization dep; §5.3 "tool controls what ends up on
// disk" lets us keep it narrow).
func slugFromHeading(text string) string {
	text = strings.ToLower(text)
	var b strings.Builder
	b.Grow(len(text))
	prevHyphen := true
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
