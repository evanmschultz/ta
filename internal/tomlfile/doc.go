// Package tomlfile reads a TOML file, records the byte ranges of every
// section, and performs surgical byte-splicing for upsert. Reads and writes
// both use tree-sitter (github.com/odvcencio/gotreesitter) to preserve every
// byte outside the touched section — including human comments and formatting.
//
// This package is the only place in ta that touches user TOML data.
package tomlfile
