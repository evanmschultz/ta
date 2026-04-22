// Package toml reads a TOML file, records the byte ranges of every
// section, and performs surgical byte-splicing for upsert. It uses a
// purpose-built pure-Go scanner that tracks string-literal and comment
// state to locate section-header boundaries — full TOML semantics are
// handled elsewhere (go-toml/v2 for schema config, the scanner plus
// canonical emission for user data files).
//
// This package is the only place in ta that touches user TOML data. It
// sits behind the record.Backend interface in internal/record. Per
// V2-PLAN §2.10 / §5.2 the Backend is schema-driven: NewBackend takes
// a slice of record.DeclaredType whose Name fields are treated as
// bracket-path prefixes. Brackets that don't match any declared prefix
// are body content of the enclosing declared record, not sibling
// records. Body range runs from a declared record's header to the
// start of the next declared record (or EOF), per V2-PLAN §2.11.
//
// The github.com/odvcencio/gotreesitter dependency is retained in go.mod
// as a candidate replacement; its TOML grammar currently fails on TOML
// multi-line strings (triple-double-quoted and triple-single-quoted),
// which are load-bearing for ta's markdown-in-TOML design. See
// docs/api-notes.md for the probe that surfaced this, and docs/ta.md
// §Open items for the decision to revisit if upstream lands multi-line
// support.
package toml

import (
	_ "github.com/odvcencio/gotreesitter" // anchored dependency; see package doc above
)
