// Package format provides the manifest-driven block extraction substrate
// for ta's template/output formats (html, md, txt). A Format implementation
// declares how to Parse a buffer into Blocks, Find a single block by name,
// Splice content into a named block while preserving the surrounding
// bytes, and Marshal blocks back to a buffer.
//
// This package is distinct from internal/backend/<format>/ which provides
// the record.Backend layer (schema-declared section store for record CRUD).
// Format is for manifest-driven extraction from arbitrary files (e.g. an
// HTML template that ta does NOT own end-to-end). record.Backend is for
// files whose section layout ta defines.
//
// Backends register their Format implementation via init() under the
// schema-enum value ("html", "md", "txt"). Get returns the implementation
// by name; the registry is NOT goroutine-safe post-init.
package format
