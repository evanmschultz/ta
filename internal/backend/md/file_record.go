package md

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/record"
)

// FileRecordBackend is the record.Backend variant for file-as-record
// md dbs (F31). Where the section-mode Backend chops a single file
// into multiple records via ATX heading boundaries, FileRecordBackend
// treats the WHOLE file as one record: frontmatter holds the typed
// fields, body holds the markdown content under one declared
// `body_field`.
//
// A FileRecordBackend serves exactly one record type; the owning db
// must declare a single file-as-record type per F31's mixed-mode
// prohibition (see internal/schema for the load-time check). The type
// must declare `body_field = "<name>"` naming the field that receives
// the markdown body.
//
// FileRecordBackend is safe for concurrent use; it holds immutable
// schema information.
type FileRecordBackend struct {
	types     []record.DeclaredType
	typeName  string
	bodyField string
}

// Compile-time assertion that *FileRecordBackend satisfies record.Backend.
var _ record.Backend = (*FileRecordBackend)(nil)

// NewFileRecordBackend constructs a FileRecordBackend for the named
// type with the named body field. typeName must match the bare type
// declared on the owning db's file-as-record type; bodyField names
// the field in the type's `fields` map that receives the markdown
// body. Heading is unused on this backend — file-as-record records
// have no per-section anchor.
//
// Returns an error when typeName or bodyField is empty (both are
// required). Loud-fail per F31 contract.
func NewFileRecordBackend(types []record.DeclaredType, typeName, bodyField string) (*FileRecordBackend, error) {
	if typeName == "" {
		return nil, fmt.Errorf("md: file-as-record backend requires non-empty type name")
	}
	if bodyField == "" {
		return nil, fmt.Errorf("md: file-as-record backend requires non-empty body field")
	}
	clone := make([]record.DeclaredType, len(types))
	copy(clone, types)
	return &FileRecordBackend{
		types:     clone,
		typeName:  typeName,
		bodyField: bodyField,
	}, nil
}

// Types returns a copy of the declared types this backend was
// constructed with. Mirrors the section-mode Backend.Types contract.
func (b *FileRecordBackend) Types() []record.DeclaredType {
	out := make([]record.DeclaredType, len(b.types))
	copy(out, b.types)
	return out
}

// BodyField returns the field name configured to receive the markdown
// body. Used by ops to route field reads/writes — the body field is
// served from the body bytes; every other declared field is served
// from the frontmatter map.
func (b *FileRecordBackend) BodyField() string {
	return b.bodyField
}

// List returns the single section address for the file IF the file
// contains a record (non-empty buffer). The file-as-record backend
// has no internal anchoring: an empty buffer yields no addresses, a
// non-empty buffer yields exactly one address (`section`).
//
// For file-as-record dbs the on-disk address structure is the
// file-relpath itself — the resolver / ops layer determines that. The
// backend reports a stable opaque label since the section parameter
// of Find/Emit/Splice is also opaque on this backend.
func (b *FileRecordBackend) List(buf []byte, scope string) ([]string, error) {
	if err := b.guardBracketHeader(buf); err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, nil
	}
	addr := b.typeName
	if scope != "" && scope != addr && !strings.HasPrefix(addr, scope+".") {
		return nil, nil
	}
	return []string{addr}, nil
}

// Find on a file-as-record buffer returns the whole buffer as the
// record's range. The section argument is accepted for interface
// compatibility but is not used to subdivide the buffer — the file
// IS the record.
//
// Empty buffer returns (zero, false, nil) so callers can distinguish
// "file present but empty" from "record found".
func (b *FileRecordBackend) Find(buf []byte, section string) (record.Section, bool, error) {
	if section == "" {
		return record.Section{}, false, fmt.Errorf("%w", ErrEmptySection)
	}
	if err := b.guardBracketHeader(buf); err != nil {
		return record.Section{}, false, err
	}
	if len(buf) == 0 {
		return record.Section{}, false, nil
	}
	return record.Section{
		Path:  section,
		Range: [2]int{0, len(buf)},
	}, true, nil
}

// Emit serializes rec to the file-as-record on-disk shape:
// `---\n<sorted frontmatter>\n---\n<body>\n`. Every declared field
// EXCEPT bodyField goes into the YAML frontmatter; bodyField's string
// value is the markdown body. Output bytes are deterministic — same
// fields produce same bytes regardless of map iteration order, per
// F31's determinism invariant.
//
// A missing bodyField in rec produces a record with empty body (the
// frontmatter alone followed by the closing fence). An empty body is
// also valid for the section-mode Backend, so this matches existing
// MD-emit behavior.
func (b *FileRecordBackend) Emit(section string, rec record.Record) ([]byte, error) {
	if section == "" {
		return nil, fmt.Errorf("%w", ErrEmptySection)
	}
	front, err := encodeFrontmatter(map[string]any(rec), b.bodyField)
	if err != nil {
		return nil, err
	}
	body, _ := rec[b.bodyField].(string)

	var buf bytes.Buffer
	buf.Write(front)
	if body != "" {
		buf.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes(), nil
}

// Splice on file-as-record is a whole-file replace: the entire buf is
// discarded and emitted is returned. There is no surgical mid-file
// mutation because the file IS the record — no other records can
// share the file.
//
// Splice returns a copy of emitted (defensively normalizes a missing
// trailing newline) so callers can safely retain ownership of the
// returned slice.
func (b *FileRecordBackend) Splice(buf []byte, section string, emitted []byte) ([]byte, error) {
	if section == "" {
		return nil, fmt.Errorf("%w", ErrEmptySection)
	}
	if len(emitted) == 0 {
		return nil, fmt.Errorf("md: splice: empty replacement")
	}
	out := make([]byte, len(emitted))
	copy(out, emitted)
	if out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, nil
}

// guardBracketHeader scans buf for a TOML-style `[<id>]` line at column
// 0. Such headers are the section-mode marker; their presence in a
// file-as-record buffer is a contract violation per F31's loud-error
// invariant. Frontmatter and body content are otherwise free-form
// markdown.
//
// The check skips lines inside a YAML frontmatter block (`---` fences)
// AND lines inside CommonMark fenced code blocks (``` or ~~~ fences).
// Without code-fence skipping, an agent prompt that documents `ta`'s
// section-mode grammar by example (e.g. a code block containing
// `[some-toml-thing]`) would loud-fail every Get/List call.
//
// Fence detection follows CommonMark §4.5: openers may be indented
// up to 3 spaces; the closer must be the same kind (``` vs ~~~) and
// at least as long as the opener (so a 4-tick outer fence containing
// a 3-tick inner fence is unambiguous). An unclosed fence at EOF is
// itself a loud failure (ErrUnclosedFence) — silently bypassing the
// rest of the buffer would defeat the bracket-guard contract.
//
// The interior of `[...]` must be F10 id-shaped — non-empty,
// containing only [A-Za-z0-9._-] — to qualify as a real bracket
// header. Whitespace or other chars defang the line: prose like
// `[citation needed]` or `[draft]` is not a section anchor and must
// not trigger the guard.
func (b *FileRecordBackend) guardBracketHeader(buf []byte) error {
	inFront := false
	inFence := false
	var fenceChar byte
	fenceLen := 0
	fenceOpenLine := 0
	pos := 0
	line := 0
	for pos < len(buf) {
		line++
		end := bytes.IndexByte(buf[pos:], '\n')
		var lineEnd int
		if end < 0 {
			lineEnd = len(buf)
		} else {
			lineEnd = pos + end
		}
		ln := bytes.TrimRight(buf[pos:lineEnd], "\r")
		switch {
		case line == 1 && bytes.Equal(ln, []byte("---")):
			inFront = true
		case inFront && bytes.Equal(ln, []byte("---")):
			inFront = false
		case !inFront:
			if !inFence {
				if char, length, ok := startsCodeFence(ln); ok {
					inFence = true
					fenceChar = char
					fenceLen = length
					fenceOpenLine = line
					break
				}
			} else {
				if endsCodeFence(ln, fenceChar, fenceLen) {
					inFence = false
					fenceChar = 0
					fenceLen = 0
					fenceOpenLine = 0
				}
				break
			}
			if len(ln) > 0 && ln[0] == '[' && isIDShapedBracket(ln) {
				return fmt.Errorf("%w: line %d: %q", ErrBracketInFileRecord, line, string(ln))
			}
		}
		if end < 0 {
			break
		}
		pos = lineEnd + 1
	}
	if inFence {
		return fmt.Errorf("%w: opened at line %d with %s",
			ErrUnclosedFence, fenceOpenLine, strings.Repeat(string(fenceChar), fenceLen))
	}
	return nil
}

// startsCodeFence reports whether ln opens a CommonMark fenced code
// block. Returns the fence character (“ ` “ or `~`) and the run
// length so endsCodeFence can enforce the §4.5 rule that a closer
// match the opener's kind AND be at least as long. Up to 3 leading
// spaces are tolerated — list-nested code fences are common in
// agent-prompt documentation.
func startsCodeFence(ln []byte) (byte, int, bool) {
	i := 0
	for i < len(ln) && i < 3 && ln[i] == ' ' {
		i++
	}
	if i >= len(ln) {
		return 0, 0, false
	}
	c := ln[i]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	run := 0
	for i < len(ln) && ln[i] == c {
		run++
		i++
	}
	if run < 3 {
		return 0, 0, false
	}
	// CommonMark forbids backticks in the info-string of a backtick
	// fence opener. A `\`\`\`...\`\`\`` line is a "compressed inline
	// fence" not a block fence; treat as no-fence.
	if c == '`' {
		for ; i < len(ln); i++ {
			if ln[i] == '`' {
				return 0, 0, false
			}
		}
	}
	return c, run, true
}

// endsCodeFence reports whether ln closes a fence opened with the
// given char and length. Per CommonMark §4.5: closer must be the same
// kind, run length ≥ opener length, and carry no info-string (only
// optional trailing whitespace). Up to 3 leading spaces tolerated on
// the closer.
func endsCodeFence(ln []byte, openChar byte, openLen int) bool {
	i := 0
	for i < len(ln) && i < 3 && ln[i] == ' ' {
		i++
	}
	run := 0
	for i < len(ln) && ln[i] == openChar {
		run++
		i++
	}
	if run < openLen {
		return false
	}
	for ; i < len(ln); i++ {
		if ln[i] != ' ' && ln[i] != '\t' {
			return false
		}
	}
	return true
}

// isIDShapedBracket reports whether ln is a strict F10-style `[<id>]`
// header. The interior must be one or more characters from
// [A-Za-z0-9._-]; any whitespace or other character means the line is
// prose, not a section anchor. Trailing whitespace after `]` is
// tolerated (we trim before the check).
func isIDShapedBracket(ln []byte) bool {
	trimmed := bytes.TrimRight(ln, " \t")
	if len(trimmed) < 3 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return false
	}
	interior := trimmed[1 : len(trimmed)-1]
	if len(interior) == 0 {
		return false
	}
	for _, c := range interior {
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
