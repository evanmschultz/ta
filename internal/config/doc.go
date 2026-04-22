// Package config resolves the schema file that governs a project.
// Reads exactly one file — <projectPath>/.ta/schema.toml — with no
// ancestor walk and no home-layer fallback (V2-PLAN §12.11 / §14.2).
//
// Schema parsing uses github.com/pelletier/go-toml/v2 — this is the only
// place that parser is used; user data files always flow through package
// internal/backend/toml.
package config
