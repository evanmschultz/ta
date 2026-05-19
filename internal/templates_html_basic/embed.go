package templates_html_basic

import (
	"embed"
	"io/fs"
)

// embeddedFS holds the Track A template tree, embedded at compile time
// from internal/templates_html_basic/templates/. The "all:" prefix
// preserves dotfiles (e.g. .gitignore) so the embedded snapshot mirrors
// the source dir byte-for-byte. Walks rooted at "templates" — callers
// that want a templates-rooted fs.FS should use EmbeddedBasicHTML.
//
//go:embed all:templates
var embeddedFS embed.FS

// EmbeddedBasicHTML returns the read-only filesystem holding the Track
// A HTML templates. The returned fs.FS is rooted at the templates/
// directory — i.e. callers see "cascade_drop.html" directly, not
// "templates/cascade_drop.html".
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
