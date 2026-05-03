package md

import (
	"bytes"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// SplitFrontmatter is the exported alias for splitFrontmatter so callers
// outside the md package (the ops field-extraction path) can use the
// same splitter.
func SplitFrontmatter(buf []byte) (front, body []byte, err error) {
	return splitFrontmatter(buf)
}

// DecodeFrontmatter is the exported alias for decodeFrontmatter so the
// ops field-extraction path can decode frontmatter without re-importing
// gopkg.in/yaml.v3 directly.
func DecodeFrontmatter(front []byte) (map[string]any, error) {
	return decodeFrontmatter(front)
}

// EncodeFrontmatter is the exported alias for encodeFrontmatter. The
// initapply F33 nested→flat agent transform needs to re-emit frontmatter
// with a rewritten `name` field while keeping byte-level determinism
// (alphabetical key order, single trailing newline) identical to what
// the file-as-record backend writes; sharing the encoder is the only
// way to avoid divergent emission paths drifting apart.
func EncodeFrontmatter(fields map[string]any, bodyField string) ([]byte, error) {
	return encodeFrontmatter(fields, bodyField)
}

// splitFrontmatter splits buf into (front, body) on the YAML
// frontmatter contract: the buffer must open with a line containing
// exactly `---` (followed by a newline) and contain a matching closing
// `---` line; everything between is `front`, everything after the
// closing fence's trailing newline is `body`.
//
// Returns (nil, buf, nil) when the buffer does not open with `---` —
// callers decide whether the absence is acceptable. File-as-record
// dbs treat absent frontmatter as ErrMissingFrontmatter; the splitter
// itself stays neutral so section-mode dbs can use the same helper to
// detect accidental frontmatter (where its presence is the violation).
//
// Returns ErrMalformedFrontmatter when the opening fence has no
// matching closing fence anywhere in buf.
//
// CRLF line endings are normalized by trimming `\r` at line ends
// during fence scanning, so a Windows-line-ending file does not
// silently appear bodyless. The returned body slice is the raw bytes
// after the closing fence (CRLF preserved); only the splitter logic
// is line-ending-tolerant.
//
// Known limitation: a YAML scalar literal-block (`|`) whose
// continuation line is exactly `---` at column 0 will steal the
// closing fence — `desc: |\n---\n...` reads with an empty front and
// `---\n...` carrying into body. This is a pathological input
// (literal markdown horizontal-rule embedded via YAML literal-block
// continuation); detecting it would require parsing YAML structure,
// out of scope for the splitter. Document and accept.
func splitFrontmatter(buf []byte) (front, body []byte, err error) {
	const fence = "---"

	if !bytes.HasPrefix(buf, []byte(fence)) {
		return nil, buf, nil
	}
	// First non-fence byte determines whether this is a real
	// frontmatter fence (`---\n` or `---\r\n`) or just a line that
	// happens to start with `---` followed by content.
	afterFence := len(fence)
	if afterFence >= len(buf) {
		return nil, nil, fmt.Errorf("%w: opening fence with no terminator", ErrMalformedFrontmatter)
	}
	switch buf[afterFence] {
	case '\n':
		afterFence++
	case '\r':
		if afterFence+1 >= len(buf) || buf[afterFence+1] != '\n' {
			return nil, buf, nil
		}
		afterFence += 2
	default:
		// `---` followed by non-newline content on the same line is not
		// a frontmatter fence; treat as no-frontmatter.
		return nil, buf, nil
	}
	frontStart := afterFence

	// Scan line-by-line for a matching `---` line (LF or CRLF).
	rest := buf[frontStart:]
	closeRel := findFenceLine(rest, fence)
	if closeRel < 0 {
		return nil, nil, fmt.Errorf("%w: unterminated frontmatter", ErrMalformedFrontmatter)
	}

	front = rest[:closeRel]
	bodyStart := frontStart + closeRel + len(fence)
	if bodyStart < len(buf) && buf[bodyStart] == '\r' {
		bodyStart++
	}
	if bodyStart < len(buf) && buf[bodyStart] == '\n' {
		bodyStart++
	}
	body = buf[bodyStart:]
	return front, body, nil
}

// findFenceLine returns the byte offset within buf at which a line
// equal to fence (followed by `\n`, `\r\n`, or EOF) begins, or -1 when
// no such line exists. Lines are split on '\n'. CRLF line endings are
// tolerated: a `\r` immediately preceding the line-terminating `\n`
// is trimmed before the equality check.
func findFenceLine(buf []byte, fence string) int {
	pos := 0
	for pos <= len(buf) {
		next := bytes.IndexByte(buf[pos:], '\n')
		var lineEnd int
		if next < 0 {
			lineEnd = len(buf)
		} else {
			lineEnd = pos + next
		}
		ln := buf[pos:lineEnd]
		ln = bytes.TrimRight(ln, "\r")
		if len(ln) == len(fence) && string(ln) == fence {
			return pos
		}
		if next < 0 {
			return -1
		}
		pos = lineEnd + 1
	}
	return -1
}

// decodeFrontmatter parses front (the YAML between the `---` fences)
// into a map[string]any. yaml.v3 returns nested maps with string keys
// directly, so no post-decode key conversion is needed.
//
// An empty front returns an empty (non-nil) map.
func decodeFrontmatter(front []byte) (map[string]any, error) {
	out := map[string]any{}
	if len(front) == 0 {
		return out, nil
	}
	if err := yaml.Unmarshal(front, &out); err != nil {
		return nil, fmt.Errorf("yaml: decode frontmatter: %w", err)
	}
	return out, nil
}

// encodeFrontmatter renders fields as YAML between `---` fences,
// terminating with a newline. Key order is alphabetical so emitted
// bytes are deterministic across map iteration orders — the F31
// determinism invariant.
//
// The bodyField key, when present in fields, is excluded from the
// frontmatter (it lives in the body, not in YAML). yaml.v3's Marshal
// of a Go map sorts keys non-deterministically across map iterations;
// to lock the order we build a yaml.Node mapping by hand with sorted
// keys.
func encodeFrontmatter(fields map[string]any, bodyField string) ([]byte, error) {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k == bodyField {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("---\n")
	if len(keys) > 0 {
		root := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range keys {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
			valNode := &yaml.Node{}
			if err := valNode.Encode(fields[k]); err != nil {
				return nil, fmt.Errorf("yaml: encode field %q: %w", k, err)
			}
			root.Content = append(root.Content, keyNode, valNode)
		}
		body, err := yaml.Marshal(root)
		if err != nil {
			return nil, fmt.Errorf("yaml: marshal frontmatter: %w", err)
		}
		buf.Write(body)
	}
	buf.WriteString("---\n")
	return buf.Bytes(), nil
}
