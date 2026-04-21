// Package schema models the ta schema config and validates agent-supplied
// section data against it.
//
// V2 shape (see docs/V2-PLAN.md §4): the top-level tables of the schema
// file are databases. Each db declares meta-fields (exactly one of file /
// directory / collection, plus format and optionally description) and
// zero or more record types under [<db>.<type>]. Each type declares
// fields under [<db>.<type>.fields.<name>].
//
// Load enforces the meta-schema (§4.7): shape-selector exclusivity,
// format values, file-extension-vs-format match, MD heading uniqueness,
// field-type support, path uniqueness and non-nesting across dbs.
//
// The validator operates on map[string]any, so it does not depend on any
// particular TOML parser. Schemas are loaded from ta's own config file (see
// package config) which is parsed with github.com/pelletier/go-toml/v2 — the
// user's TOML data files are never touched by that parser.
package schema
