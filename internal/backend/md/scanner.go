package md

import (
	"fmt"
	"sort"
	"strings"
)

// Heading is one ATX heading discovered by scanATX.
//
// LineStart / LineEnd are 1-indexed inclusive line numbers for the
// heading's own line.
//
// Slug is the kebab-slug derived from the heading text; Chain is the
// ordered slugs of this heading's declared ancestors (shallowest first)
// PLUS this heading's own slug last. Under V2-PLAN §2.11 / §5.3.2
// (2026-04-21 refinement) the record address for this heading is
// "<type-name>.<chain-joined-with-dots>" where type-name is looked up
// from the owning Backend by this heading's Level. Address carries the
// pre-joined result so callers that already know the type do not have
// to re-compose it.
//
// ByteRange is the [start, end) byte offsets of this declared heading's
// section span — from the beginning of the heading's line to the start
// of the next heading at the SAME OR SHALLOWER declared level, or EOF
// for the last such heading. Deeper declared headings under this one
// are BOTH body bytes of this record AND addressable records in their
// own right with narrower nested ranges.
type Heading struct {
	Level     int
	Text      string
	Slug      string
	Chain     []string
	Address   string
	LineStart int
	LineEnd   int
	ByteRange [2]int
}

// scanATX walks buf and returns every DECLARED ATX heading in source
// order. A heading is declared when its level matches one of the keys
// in typeByLevel. Non-declared headings are fence-aware skipped and do
// not contribute to addresses — they are body content of the enclosing
// declared ancestor per V2-PLAN §5.3.2.
//
// Addressing (V2-PLAN §5.3.2 / §5.5, 2026-04-21 hierarchical refinement):
//
//   - A scan-time stack maps each declared level to its current slug.
//   - When a heading at declared level N is encountered, stack slots at
//     levels > N are cleared; stack[N] becomes this heading's slug.
//   - The Chain for this heading = slugs currently in the stack at
//     declared levels <= N, in shallowest-to-deepest order, inclusive
//     of the just-set self slot. Empty slots (declared ancestor level
//     with no heading yet seen — the "orphan H3 under H1 with missing
//     H2" case) are skipped; chain keeps only slugs actually present.
//   - Address = typeByLevel[N] + "." + strings.Join(Chain, ".").
//
// Byte ranges (V2-PLAN §2.11 hierarchical refinement): a declared
// heading's range runs from its heading line to the start of the next
// heading at the SAME OR SHALLOWER declared level, or EOF. Deeper
// declared headings between the two are part of this heading's body
// bytes AND have their own narrower nested ranges.
//
// Fence-state tracking is unchanged: `#` lines inside ```` ``` ```` or
// `~~~` fences are never treated as headings, declared or otherwise.
//
// Slug-collision detection is per FULL ADDRESS: two declared headings
// that produce the same chain-resolved address return ErrSlugCollision.
// Two H3 "prereqs" under the same H2 collide; two H3 "prereqs" under
// different H2 parents do not (different parent slugs → different
// addresses). Collisions inside non-declared levels are ignored — those
// slugs never compose a record address.
//
// When typeByLevel is empty the result is always empty (no boundary
// anywhere; everything is content).
func scanATX(buf []byte, typeByLevel map[int]string) ([]Heading, error) {
	var out []Heading
	if len(typeByLevel) == 0 {
		return out, nil
	}

	// declaredSorted is the sorted list of declared levels so we can
	// iterate "shallower than N" in order when building a chain.
	declaredSorted := make([]int, 0, len(typeByLevel))
	for lvl := range typeByLevel {
		declaredSorted = append(declaredSorted, lvl)
	}
	sort.Ints(declaredSorted)

	// stack maps declared level -> current slug (empty string when no
	// heading at that level has been seen since the most recent
	// shallower ancestor).
	stack := make(map[int]string, len(typeByLevel))

	// Fence-state.
	inFence := false
	fenceChar := byte(0)
	fenceLen := 0

	line := 1
	lineStart := 0
	n := len(buf)

	for i := 0; i <= n; i++ {
		if i == 0 || (i > 0 && buf[i-1] == '\n') {
			lineStart = i
			if fc, flen, ok := readFenceLine(buf, lineStart); ok {
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
				if lvl, text, ok := readATXHeading(buf, lineStart); ok {
					typeName, declared := typeByLevel[lvl]
					if declared && typeName != "" {
						slug := slugFromHeading(text)
						if slug != "" {
							// Update stack: clear deeper declared levels,
							// set self.
							for _, dl := range declaredSorted {
								if dl > lvl {
									delete(stack, dl)
								}
							}
							stack[lvl] = slug

							// Chain = slugs at declared levels <= lvl,
							// in shallowest-first order, skipping empty
							// slots (missing-ancestor orphan rule).
							chain := make([]string, 0, len(declaredSorted))
							for _, dl := range declaredSorted {
								if dl > lvl {
									break
								}
								if s, ok := stack[dl]; ok && s != "" {
									chain = append(chain, s)
								}
							}
							addr := typeName + "." + strings.Join(chain, ".")
							h := Heading{
								Level:     lvl,
								Text:      text,
								Slug:      slug,
								Chain:     chain,
								Address:   addr,
								LineStart: line,
								LineEnd:   line,
							}
							h.ByteRange[0] = lineStart
							out = append(out, h)
						}
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

	// Patch ByteRange.End: end at next heading whose Level <= self.Level.
	// If none found, EOF.
	for idx := range out {
		self := out[idx]
		end := n
		for j := idx + 1; j < len(out); j++ {
			if out[j].Level <= self.Level {
				end = out[j].ByteRange[0]
				break
			}
		}
		out[idx].ByteRange[1] = end
	}

	// Collision check on full address.
	seen := make(map[string]int, len(out))
	for _, h := range out {
		if first, dup := seen[h.Address]; dup {
			return nil, fmt.Errorf("%w: address=%q at lines %d and %d",
				ErrSlugCollision, h.Address, first, h.LineStart)
		}
		seen[h.Address] = h.LineStart
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
	return c, runLen, true
}

// stripTrailingHashes removes a trailing run of `#` characters that is
// either at end-of-string or preceded by whitespace. CommonMark rule:
// trailing hashes are optional decoration and not part of the heading
// text.
func stripTrailingHashes(s string) string {
	trimmed := strings.TrimRight(s, " \t")
	end := len(trimmed)
	j := end
	for j > 0 && trimmed[j-1] == '#' {
		j--
	}
	if j == end {
		return s
	}
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
