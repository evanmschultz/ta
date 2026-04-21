package schema

import _ "embed"

// MetaSchemaTOML is the canonical meta-schema document: a literal
// description of the schema language itself, surfaced via the `schema`
// tool's `scope = "ta_schema"` (V2-PLAN §3.3, §12.2). It is the single
// source of truth an agent reads to construct a valid `schema(action=
// "create"|"update", kind, name, data)` call.
//
// The meta-schema is embedded at compile time from meta_schema.toml and
// never read from disk at runtime; callers can rely on it being present
// in every binary.
//
//go:embed meta_schema.toml
var MetaSchemaTOML string

// MetaSchemaPath is the scope identifier that selects the meta-schema
// via `schema(action="get", scope="ta_schema")`.
const MetaSchemaPath = "ta_schema"
