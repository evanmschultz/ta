package tomlfile

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Section describes a TOML section (or array-of-tables entry) within a file,
// with byte ranges that make surgical splicing possible.
//
// Byte layout from lowest to highest offset is:
//
//	HeadRange.Start == Range.Start
//	HeadRange.End   == HeaderRange.Start     (leading comment block attached
//	                                          to this header, possibly empty)
//	HeaderRange.End == BodyRange.Start       (the '[path]' line + its '\n')
//	BodyRange.End   == Range.End             (body content, trailing blank
//	                                          lines and the next section's
//	                                          leading comment block are NOT
//	                                          included here)
type Section struct {
	// Path is the bracketed path, e.g. "task.task_001" for [task.task_001]
	// or "notes" for [[notes]]. Surrounding whitespace inside the brackets
	// is trimmed.
	Path string
	// ArrayOfTables is true if the section was declared with [[...]].
	ArrayOfTables bool
	// Range is [start, end) bytes of everything this section owns: leading
	// comment block, header line, and body (no trailing blank separator or
	// next section's leading comments).
	Range [2]int
	// HeadRange is [start, end) bytes of the leading comment block —
	// comment lines directly above the header with no blank line between
	// the last comment and the header. Empty when no such block exists;
	// in that case HeadRange.Start == HeadRange.End == HeaderRange.Start.
	HeadRange [2]int
	// HeaderRange is [start, end) bytes of the header line including the
	// trailing newline (or EOF if the header is on the last line).
	HeaderRange [2]int
	// BodyRange is [start, end) bytes of the section body — everything
	// between HeaderRange.End and the last content newline before the next
	// section's leading comment block (or EOF).
	BodyRange [2]int
}

// File is a parsed TOML file: the raw byte buffer plus discovered sections.
// The zero value is not useful; construct via Parse or ParseBytes.
type File struct {
	// Path is the filesystem path recorded at parse time. It is never
	// re-read during splicing; callers route writes back through WriteAtomic.
	Path string
	// Buf is the raw file bytes. Splice operates on Buf byte-for-byte so it
	// must not be mutated after parsing.
	Buf []byte
	// Sections lists every discovered section in file order.
	Sections []Section
}

// ErrNotExist is returned (wrapped) by Parse when the target file does not
// exist. Callers can branch on errors.Is(err, ErrNotExist) to treat the
// missing-file case as "create on first write".
var ErrNotExist = fs.ErrNotExist

// Parse reads the file at path and returns the parsed File. A missing file
// yields an error that wraps ErrNotExist.
func Parse(path string) (*File, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tomlfile: read %s: %w", path, err)
	}
	return ParseBytes(path, buf)
}

// ParseBytes parses an in-memory TOML buffer. path is recorded on the File
// for later write calls; it is not read from disk.
func ParseBytes(path string, buf []byte) (*File, error) {
	headers, err := scanSections(buf)
	if err != nil {
		return nil, fmt.Errorf("tomlfile: %s: %w", path, err)
	}
	return &File{
		Path:     path,
		Buf:      buf,
		Sections: buildSections(buf, headers),
	}, nil
}

// Find locates the first section whose Path equals target. It returns the
// section and true, or the zero value and false if not found.
func (f *File) Find(target string) (Section, bool) {
	for _, s := range f.Sections {
		if s.Path == target {
			return s, true
		}
	}
	return Section{}, false
}

// Paths returns the Path of every discovered section in file order.
func (f *File) Paths() []string {
	paths := make([]string, len(f.Sections))
	for i, s := range f.Sections {
		paths[i] = s.Path
	}
	return paths
}

type sectionHeader struct {
	path            string
	isArrayOfTables bool
	startByte       int
	headerEndByte   int
}

func buildSections(buf []byte, headers []sectionHeader) []Section {
	sections := make([]Section, len(headers))
	leadStarts := make([]int, len(headers))
	for i, h := range headers {
		leadStarts[i] = scanLeadingCommentStart(buf, h.startByte)
	}
	for i, h := range headers {
		var nextLead int
		if i+1 < len(headers) {
			nextLead = leadStarts[i+1]
		} else {
			nextLead = len(buf)
		}
		bodyEnd := scanBodyEnd(buf, h.headerEndByte, nextLead)
		sections[i] = Section{
			Path:          h.path,
			ArrayOfTables: h.isArrayOfTables,
			Range:         [2]int{leadStarts[i], bodyEnd},
			HeadRange:     [2]int{leadStarts[i], h.startByte},
			HeaderRange:   [2]int{h.startByte, h.headerEndByte},
			BodyRange:     [2]int{h.headerEndByte, bodyEnd},
		}
	}
	return sections
}

// scanLeadingCommentStart walks back from headerStart to find the start of
// the leading comment block attached to this header. A comment line is
// "attached" when the line immediately above the header is a comment (no
// blank line between); the block extends upward through adjacent comment
// lines, stopping at a blank line, non-comment content, or BOF.
// Returns headerStart when no leading block exists.
func scanLeadingCommentStart(buf []byte, headerStart int) int {
	if headerStart == 0 || buf[headerStart-1] != '\n' {
		return headerStart
	}
	blockStart := headerStart
	lineEnd := headerStart - 1
	for {
		lineStart := lineEnd
		for lineStart > 0 && buf[lineStart-1] != '\n' {
			lineStart--
		}
		if !isCommentLine(buf, lineStart, lineEnd) {
			break
		}
		blockStart = lineStart
		if lineStart == 0 {
			break
		}
		lineEnd = lineStart - 1
	}
	return blockStart
}

// scanBodyEnd returns the offset after the last content (key-value) line of
// a section body. It walks back from nextLeadStart, skipping trailing blank
// lines and comment lines so that stranded comments and blank separators
// between sections survive UPDATE splicing unchanged. The first non-blank,
// non-comment line encountered defines the body's logical end; the returned
// offset is just past that line's trailing newline.
//
// Trailing comments inside a section that end up "relocated" after a new
// body are preserved bytes, never regenerated ones — this keeps byte-identity
// for human-authored content even though a reader may now read them as
// attached to the new body rather than the old one.
//
// If the entire span between headerEnd and nextLeadStart is blank or
// comment-only, scanBodyEnd returns headerEnd.
func scanBodyEnd(buf []byte, headerEnd, nextLeadStart int) int {
	i := nextLeadStart
	for i > headerEnd {
		if buf[i-1] != '\n' {
			return i
		}
		lineStart := i - 1
		for lineStart > headerEnd && buf[lineStart-1] != '\n' {
			lineStart--
		}
		if !isBlankLine(buf, lineStart, i-1) && !isCommentLine(buf, lineStart, i-1) {
			return i
		}
		i = lineStart
	}
	return headerEnd
}

func isCommentLine(buf []byte, lineStart, lineEnd int) bool {
	j := lineStart
	for j < lineEnd && (buf[j] == ' ' || buf[j] == '\t') {
		j++
	}
	return j < lineEnd && buf[j] == '#'
}

func isBlankLine(buf []byte, lineStart, lineEnd int) bool {
	for j := lineStart; j < lineEnd; j++ {
		if buf[j] != ' ' && buf[j] != '\t' {
			return false
		}
	}
	return true
}

// scanSections walks buf and records every TOML section header it finds.
// It tracks string-literal and comment state so characters that would
// otherwise look like a header start ('[' at line start) are ignored when
// they appear inside a string or comment.
func scanSections(buf []byte) ([]sectionHeader, error) {
	var out []sectionHeader
	n := len(buf)
	atLineStart := true
	for i := 0; i < n; {
		if atLineStart {
			j := i
			for j < n && (buf[j] == ' ' || buf[j] == '\t') {
				j++
			}
			if j < n && buf[j] == '[' {
				hdr, next, err := parseHeaderAt(buf, j)
				if err != nil {
					return nil, err
				}
				out = append(out, hdr)
				i = next
				atLineStart = true
				continue
			}
			atLineStart = false
		}

		switch buf[i] {
		case '\n':
			i++
			atLineStart = true
		case '#':
			for i < n && buf[i] != '\n' {
				i++
			}
		case '"':
			end, err := endOfString(buf, i, '"')
			if err != nil {
				return nil, err
			}
			i = end
		case '\'':
			end, err := endOfString(buf, i, '\'')
			if err != nil {
				return nil, err
			}
			i = end
		default:
			i++
		}
	}
	return out, nil
}

func parseHeaderAt(buf []byte, start int) (sectionHeader, int, error) {
	n := len(buf)
	i := start + 1
	isArray := false
	if i < n && buf[i] == '[' {
		isArray = true
		i++
	}
	keyStart := i
	for i < n {
		switch buf[i] {
		case '"':
			end, err := endOfString(buf, i, '"')
			if err != nil {
				return sectionHeader{}, 0, fmt.Errorf("header at byte %d: %w", start, err)
			}
			i = end
		case '\'':
			end, err := endOfString(buf, i, '\'')
			if err != nil {
				return sectionHeader{}, 0, fmt.Errorf("header at byte %d: %w", start, err)
			}
			i = end
		case ']':
			keyEnd := i
			i++
			if isArray {
				if i >= n || buf[i] != ']' {
					return sectionHeader{}, 0, fmt.Errorf("section header at byte %d: '[[' not closed by ']]'", start)
				}
				i++
			}
			key := strings.TrimSpace(string(buf[keyStart:keyEnd]))
			if key == "" {
				return sectionHeader{}, 0, fmt.Errorf("section header at byte %d: empty key", start)
			}
			headerEnd := advanceToNewline(buf, i)
			return sectionHeader{
				path:            key,
				isArrayOfTables: isArray,
				startByte:       start,
				headerEndByte:   headerEnd,
			}, headerEnd, nil
		case '\n':
			return sectionHeader{}, 0, fmt.Errorf("section header at byte %d: unterminated", start)
		default:
			i++
		}
	}
	return sectionHeader{}, 0, fmt.Errorf("section header at byte %d: unterminated at EOF", start)
}

// endOfString returns the index just past the end of the string literal that
// starts at buf[start] with quote as the opener. It handles single-line and
// multi-line forms for both basic (") and literal (') strings.
func endOfString(buf []byte, start int, quote byte) (int, error) {
	n := len(buf)
	if start+2 < n && buf[start+1] == quote && buf[start+2] == quote {
		for i := start + 3; i+2 < n; i++ {
			if buf[i] == quote && buf[i+1] == quote && buf[i+2] == quote {
				return i + 3, nil
			}
		}
		return 0, fmt.Errorf("unterminated multi-line string starting at byte %d", start)
	}
	i := start + 1
	for i < n {
		switch buf[i] {
		case '\\':
			if quote == '"' {
				i += 2
				continue
			}
			i++
		case quote:
			return i + 1, nil
		case '\n':
			return 0, fmt.Errorf("unterminated string starting at byte %d", start)
		default:
			i++
		}
	}
	return 0, fmt.Errorf("unterminated string starting at byte %d", start)
}

func advanceToNewline(buf []byte, start int) int {
	for i := start; i < len(buf); i++ {
		if buf[i] == '\n' {
			return i + 1
		}
	}
	return len(buf)
}
