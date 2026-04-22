package db

import (
	"path/filepath"
	"strings"
)

// kebabCase lowercases s, replaces any run of non-[a-z0-9] characters
// with a single '-', and trims leading/trailing hyphens. Non-ASCII
// letters are dropped (they become separators); this keeps slugs stable
// across filesystems without pulling in a unicode-normalisation
// dependency. Document this behaviour in the exported helpers that call
// into kebabCase.
func kebabCase(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := true // start state: suppress leading hyphens
	for i := range len(s) {
		c := s[i]
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
	out := b.String()
	out = strings.TrimRight(out, "-")
	return out
}

// slugFromCollectionPath derives an instance slug from a
// path-relative-to-collection-root by stripping the extension, splitting
// on the OS path separator, kebab-casing each segment, and rejoining
// with '-'. ext is the declared format extension without leading dot
// (e.g. "md"). Per V2-PLAN §5.5.2 table.
func slugFromCollectionPath(relPath, ext string) string {
	// Normalize to forward slashes so Windows-style separators are handled.
	norm := filepath.ToSlash(relPath)
	// Strip extension.
	dotExt := "." + ext
	if strings.HasSuffix(strings.ToLower(norm), dotExt) {
		norm = norm[:len(norm)-len(dotExt)]
	}
	parts := strings.Split(norm, "/")
	for i, p := range parts {
		parts[i] = kebabCase(p)
	}
	// Drop empty segments produced by leading/trailing slashes.
	nonEmpty := parts[:0]
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "-")
}
