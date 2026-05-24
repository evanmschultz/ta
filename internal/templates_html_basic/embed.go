package templates_html_basic

import (
	"embed"
	"io/fs"
)

// embeddedFS holds the Track A template tree and partials, embedded at compile time
// from internal/templates_html_basic/templates/ and internal/templates_html_basic/partials/.
// The "all:" prefix preserves dotfiles (e.g. .gitignore) so the embedded snapshot
// mirrors the source dirs byte-for-byte. Both directories are embedded at the root
// so they're accessible as "templates/*" and "partials/*" for ParseFS.
//
//go:embed all:templates all:partials
var embeddedFS embed.FS

// EmbeddedBasicHTML returns the read-only filesystem holding the Track A HTML
// templates. The returned fs.FS is rooted at the templates/ directory — i.e.
// callers see "cascade_drop.html" directly, not "templates/cascade_drop.html".
//
// The filesystem is safe to share across goroutines; embed.FS values
// are immutable and concurrency-safe by construction.
func EmbeddedBasicHTML() fs.FS {
	sub, err := fs.Sub(embeddedFS, "templates")
	if err != nil {
		// fs.Sub only errors on an invalid path; "templates" is a fixed
		// literal that mirrors the //go:embed directive above, so an
		// error here would indicate a compile-time misconfiguration
		// caught by package init in tests. Panic is the right signal —
		// the package cannot function with a broken embed root.
		panic("templates_html_basic: fs.Sub on embedded FS: " + err.Error())
	}
	return sub
}

// embeddedFSRoot returns the root embedded FS containing both templates/ and partials/ directories.
// This is used by Render() to load the full template set with ParseFS.
func embeddedFSRoot() fs.FS {
	return embeddedFS
}
