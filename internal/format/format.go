package format

import (
	"errors"
	"fmt"
)

// Block holds one manifest-named section extracted from a file buffer.
// Bytes contains the section content (NOT the surrounding context).
// Start/End are byte offsets into the ORIGINAL buffer enabling Splice to
// enforce the preserve-outside-[Start,End) invariant.
type Block struct {
	Name  string
	Bytes []byte
	Start int
	End   int
}

// Blocks is an ordered slice of Block in source traversal order.
type Blocks []Block

// Manifest declares the minimal contract every per-format manifest provides
// for the Format implementation to resolve selector → block-name mappings.
// Concrete impls (HtmlManifest, MdManifest, TxtManifest) reside in their
// respective backend packages.
type Manifest interface {
	// BlockName maps an opaque parser node (e.g. *html.Node, *md.Heading)
	// to the manifest-declared block name. Returns (name, true) on match
	// or ("", false) on no-match.
	BlockName(node any) (string, bool)
	// Selectors returns the ordered list of selector strings the manifest
	// declares, for enumeration / validation.
	Selectors() []string
}

// Format declares the four-method contract a per-format backend implements.
type Format interface {
	Parse(buf []byte, m Manifest) (Blocks, error)
	Find(buf []byte, m Manifest, name string) ([]byte, error)
	Splice(buf []byte, m Manifest, name string, content []byte) ([]byte, error)
	Marshal(blocks Blocks, m Manifest) ([]byte, error)
}

// ErrBlockNotFound is the sentinel returned by Find and Splice when the
// requested block name does not match any selector in the input. Callers
// use errors.Is to detect this case.
var ErrBlockNotFound = errors.New("format: block not found")

// registry holds the per-format implementations. NOT goroutine-safe —
// Register MUST be called during package init() only. Panics on duplicate
// names per stdlib precedent (database/sql.Register, image.RegisterFormat).
var registry = make(map[string]Format)

// Register associates a name (the schema-enum value, e.g. "html", "md",
// "txt") with a Format implementation. Init-time only. Panics on
// duplicate registration.
func Register(name string, f Format) {
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("format: Register called twice for format %q", name))
	}
	registry[name] = f
}

// Get returns the Format implementation registered under name, or an error
// if no implementation is registered.
func Get(name string) (Format, error) {
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("format: no implementation registered for %q", name)
	}
	return f, nil
}
