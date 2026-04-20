package tomlfile

import (
	"bytes"
	"fmt"
)

// Splice returns a new buffer with the named section replaced by replacement.
// If the section does not exist, replacement is appended to the buffer with a
// blank-line separator if needed.
//
// Byte-identity invariant (load-bearing for ta's whole design):
// every byte of f.Buf that falls outside the target section's Range is copied
// through to the output unchanged. Comments, whitespace, unrelated sections,
// literal and multi-line strings, and header ordering are all preserved
// exactly as the user wrote them. Only the bytes of the target section
// itself — header through the boundary before the next section — are
// rewritten, and exactly one trailing newline that separated the section
// from what followed is kept. The replacement content is assumed to be
// canonicalized by the caller (typically via EmitSection).
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

	contentEnd := s.Range[1]
	for contentEnd > s.HeaderRange[0] && f.Buf[contentEnd-1] == '\n' {
		contentEnd--
	}
	if contentEnd < s.Range[1] {
		contentEnd++
	}

	out := make([]byte, 0, s.Range[0]+len(rep)+(len(f.Buf)-s.Range[1])+(s.Range[1]-contentEnd))
	out = append(out, f.Buf[:s.Range[0]]...)
	out = append(out, rep...)
	out = append(out, f.Buf[contentEnd:]...)
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
