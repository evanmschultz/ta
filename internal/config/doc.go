// Package config resolves the schema config file that governs a given TOML
// data file. It walks up from the data file's directory looking for
// .ta/config.toml; if none is found it falls back to ~/.ta/config.toml.
//
// Config parsing uses github.com/pelletier/go-toml/v2 — this is the only
// place that parser is used; user data files always flow through package
// tomlfile.
package config
