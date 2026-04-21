package toml

import (
	"bytes"
	"fmt"
)

// Splice returns a new buffer with the named section replaced by replacement.
// If the section does not exist, replacement is appended to the buffer with a
// blank-line separator if needed.
//
// Byte-identity invariant (load-bearing for ta's whole design):
// every byte of f.Buf that falls outside the target section's [HeaderRange.Start,
// BodyRange.End) span is copied through to the output unchanged. This preserves:
//   - file-level content before the first section,
//   - the target section's own leading comment block (HeadRange),
//   - blank-line separators between sections,
//   - the next section's leading comment block (its HeadRange),
//   - all bytes of unrelated sections.
//
// Only the header line and body of the target section are rewritten. The
// replacement is assumed to be canonicalized by the caller (typically via
// EmitSection) and therefore does not carry a leading comment.
//
// This function never mutates f.Buf.
func (f *File) Splice(sectionPath string, replacement []byte) ([]byte, error) {
	if sectionPath == "" {
		return nil, fmt.Errorf("splice: empty section path")
	}
	if len(replacement) == 0 {
		return nil, fmt.Errorf("splice: empty replacement")
	}
	s, ok := f.Find(sectionPath)
	if !ok {
		return f.appendSection(replacement), nil
	}
	rep := replacement
	if rep[len(rep)-1] != '\n' {
		rep = append(append([]byte{}, rep...), '\n')
	}

	out := make([]byte, 0, len(f.Buf)+len(rep))
	out = append(out, f.Buf[:s.HeaderRange[0]]...)
	out = append(out, rep...)
	out = append(out, f.Buf[s.BodyRange[1]:]...)
	return out, nil
}

func (f *File) appendSection(replacement []byte) []byte {
	rep := replacement
	if rep[len(rep)-1] != '\n' {
		rep = append(append([]byte{}, rep...), '\n')
	}
	if len(f.Buf) == 0 {
		return append([]byte{}, rep...)
	}
	var sep []byte
	switch {
	case !bytes.HasSuffix(f.Buf, []byte("\n")):
		sep = []byte("\n\n")
	case !bytes.HasSuffix(f.Buf, []byte("\n\n")):
		sep = []byte("\n")
	}
	out := make([]byte, 0, len(f.Buf)+len(sep)+len(rep))
	out = append(out, f.Buf...)
	out = append(out, sep...)
	out = append(out, rep...)
	return out
}
