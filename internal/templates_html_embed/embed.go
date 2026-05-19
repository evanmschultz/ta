package templates_html_embed

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// EmbeddedEmbedHTML returns the Astro-built HTML template tree rooted
// at the dist/ directory (Track B; html-embed). The returned fs.FS is
// rebased via fs.Sub so callers see dist/ contents at the root —
// e.g. an embedded file dist/index.html is reachable as "index.html".
//
// At fresh clone the FS contains only a .keep placeholder; after a
// successful `mage TemplatesBuildEmbed` it contains the Astro build
// output baked in at compile time.
func EmbeddedEmbedHTML() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// fs.Sub only errors when the given name is not a valid
		// path; "dist" is a static literal, so this is unreachable
		// under any compile-successful build. Panic loud so any
		// future refactor that breaks the invariant fails fast.
		panic(fmt.Sprintf("templates_html_embed: fs.Sub(distFS, \"dist\"): %v", err))
	}
	return sub
}
