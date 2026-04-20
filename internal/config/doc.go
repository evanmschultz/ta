// Package config resolves a schema registry for a given TOML file path by
// walking up from that path's directory looking for .ta/config.toml, then
// falling back to ~/.ta/config.toml. Config parsing uses
// github.com/pelletier/go-toml/v2 — this is the only place that parser is
// used; user data files always flow through package tomlfile.
package config
