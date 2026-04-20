// Package tomlfile reads a TOML file, records the byte ranges of every
// section, and performs surgical byte-splicing for upsert. It uses a
// purpose-built pure-Go scanner that tracks string-literal and comment
// state to locate section-header boundaries — full TOML semantics are
// handled elsewhere (go-toml/v2 for schema config, the scanner plus
// canonical emission for user data files).
//
// This package is the only place in ta that touches user TOML data.
//
// The github.com/odvcencio/gotreesitter dependency is retained in go.mod
// as a candidate replacement; its TOML grammar currently fails on TOML
// multi-line strings (triple-double-quoted and triple-single-quoted),
// which are load-bearing
// for ta's markdown-in-TOML design. See docs/api-notes.md for the probe
// that surfaced this, and docs/ta.md §Open items for the decision to
// revisit if upstream lands multi-line support.
package tomlfile

import (
	_ "github.com/odvcencio/gotreesitter" // anchored dependency; see package doc above
)
