// Package schema models the ta schema config and validates agent-supplied
// section data against it.
//
// The validator operates on map[string]any, so it does not depend on any
// particular TOML parser. Schemas are loaded from ta's own config file (see
// package config) which is parsed with github.com/pelletier/go-toml/v2 — the
// user's TOML data files are never touched by that parser.
package schema
