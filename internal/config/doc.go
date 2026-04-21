// Package config resolves the schema file that governs a given data file.
// It walks up from the data file's directory looking for .ta/schema.toml;
// if none is found it falls back to ~/.ta/schema.toml. Every schema file
// encountered is cascade-merged root-to-file per V2-PLAN §4.4.
//
// Schema parsing uses github.com/pelletier/go-toml/v2 — this is the only
// place that parser is used; user data files always flow through package
// internal/backend/toml.
package config
