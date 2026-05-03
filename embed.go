// Package ta exposes the binary-embedded `examples/` tree to CLI and
// MCP consumers. The directory lives at the repo root so it stays
// discoverable to humans browsing the source; the embed directive
// rooted here is the single mount point. Consumers (cmd/ta/main.go,
// internal/templates) receive the FS via DI rather than importing
// embed paths directly — see templates.SetBinarySource.
//
// This file is the ONLY non-mage Go source at the repo root. The
// magefile (`magefile.go`) carries `//go:build mage` so the two
// packages do not collide under any build configuration: a normal
// `go build ./...` sees package `ta` here; mage sees package `main`.
package ta

import (
	"embed"
	"io/fs"
)

//go:embed all:examples
var embeddedExamples embed.FS

// EmbeddedExamples returns the binary-embedded `examples/` tree
// rooted at "examples". Callers walk subdirectories
// (`examples/schemas/*.toml`, `examples/agents/<group>/*.md`,
// `examples/configs/*`, `examples/docs-templates/*.md`) via the
// returned fs.FS.
//
// The `.keep` sentinel files under empty subdirs are an intentional
// part of the FS surface — the templates package filters them out at
// enumeration time so callers do not see them as templates.
func EmbeddedExamples() fs.FS {
	return embeddedExamples
}
