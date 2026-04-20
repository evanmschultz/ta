package tomlfile

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Section describes a TOML section (or array-of-tables entry) within a file,
// with byte ranges that make surgical splicing possible.
type Section struct {
	// Path is the bracketed path, e.g. "task.task_001" for [task.task_001]
	// or "notes" for [[notes]]. Surrounding whitespace inside the brackets
	// is trimmed.
	Path string
	// ArrayOfTables is true if the section was declared with [[...]].
	ArrayOfTables bool
	// Range is [start, end) bytes of the entire section: the opening '['
	// through the byte before the next section header or EOF.
	Range [2]int
	// HeaderRange is [start, end) bytes of the header line including the
	// trailing newline (or EOF if the header is on the last line).
	HeaderRange [2]int
	// BodyRange is [start, end) bytes of the section body — everything
	// between HeaderRange.End and Range.End.
	BodyRange [2]int
}

// File is a parsed TOML file: the raw byte buffer plus discovered sections.
// The zero value is not useful; construct via Parse or ParseBytes.
type File struct {
	Path     string
	Buf      []byte
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
	for i, h := range headers {
		endByte := len(buf)
		if i+1 < len(headers) {
			endByte = headers[i+1].startByte
		}
		sections[i] = Section{
			Path:          h.path,
			ArrayOfTables: h.isArrayOfTables,
			Range:         [2]int{h.startByte, endByte},
			HeaderRange:   [2]int{h.startByte, h.headerEndByte},
			BodyRange:     [2]int{h.headerEndByte, endByte},
		}
	}
	return sections
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
